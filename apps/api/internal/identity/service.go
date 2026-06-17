// Package identity implements signup/login/refresh and organization management.
package identity

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/auth"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/store"
)

// Errors returned by the identity service.
var (
	ErrEmailTaken         = errors.New("identity: email already registered")
	ErrInvalidCredentials = errors.New("identity: invalid email or password")
	ErrValidation         = errors.New("identity: validation failed")
	ErrForbidden          = errors.New("identity: forbidden")
	ErrInvitationInvalid  = errors.New("identity: invitation is invalid or expired")
)

// Service holds identity business logic.
type Service struct {
	store  store.Store
	tokens *auth.TokenManager
	idgen  func() string
	now    func() time.Time
}

// NewService builds an identity service backed by the given store and token manager.
func NewService(s store.Store, tm *auth.TokenManager) *Service {
	return &Service{store: s, tokens: tm, idgen: uuid.NewString, now: time.Now}
}

// AuthResult is the outcome of a successful authentication.
type AuthResult struct {
	User    *domain.User
	Access  string
	Refresh string
}

var emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// Signup registers a new user, creates their personal organization (as owner),
// and returns an authenticated session.
func (s *Service) Signup(ctx context.Context, email, name, password string) (*AuthResult, error) {
	email = normalizeEmail(email)
	name = strings.TrimSpace(name)
	if !emailRe.MatchString(email) {
		return nil, fmt.Errorf("%w: invalid email", ErrValidation)
	}
	if len(password) < 8 {
		return nil, fmt.Errorf("%w: password must be at least 8 characters", ErrValidation)
	}
	// bcrypt silently ignores bytes beyond 72; reject rather than truncate so the
	// failure is explicit (a 400, not a surprising 500 from the hasher).
	if len(password) > 72 {
		return nil, fmt.Errorf("%w: password must be at most 72 bytes", ErrValidation)
	}
	if name == "" {
		name = strings.SplitN(email, "@", 2)[0]
	}

	if _, err := s.store.GetUserByEmail(ctx, email); err == nil {
		return nil, ErrEmailTaken
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		return nil, err
	}
	user := &domain.User{
		ID:           s.idgen(),
		Email:        email,
		Name:         name,
		PasswordHash: hash,
		CreatedAt:    s.now(),
	}
	if err := s.store.CreateUser(ctx, user); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return nil, ErrEmailTaken
		}
		return nil, err
	}

	org := &domain.Organization{
		ID:        s.idgen(),
		Name:      personalOrgName(name),
		Slug:      slugify(name) + "-" + shortID(user.ID),
		CreatedAt: s.now(),
	}
	if err := s.store.CreateOrganization(ctx, org); err != nil {
		return nil, err
	}
	if err := s.store.AddMembership(ctx, domain.Membership{OrgID: org.ID, UserID: user.ID, Role: domain.RoleOwner}); err != nil {
		return nil, err
	}
	if _, err := s.createDefaultProject(ctx, org.ID); err != nil {
		return nil, err
	}

	return s.issue(user)
}

// Login authenticates by email + password.
func (s *Service) Login(ctx context.Context, email, password string) (*AuthResult, error) {
	user, err := s.store.GetUserByEmail(ctx, normalizeEmail(email))
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}
	if !auth.CheckPassword(user.PasswordHash, password) {
		return nil, ErrInvalidCredentials
	}
	return s.issue(user)
}

// Refresh exchanges a valid refresh token for a fresh token pair.
func (s *Service) Refresh(ctx context.Context, refreshToken string) (*AuthResult, error) {
	claims, err := s.tokens.Verify(refreshToken, auth.RefreshToken)
	if err != nil {
		return nil, ErrInvalidCredentials
	}
	user, err := s.store.GetUserByID(ctx, claims.Subject)
	if err != nil {
		return nil, ErrInvalidCredentials
	}
	return s.issue(user)
}

// GetUser returns a user by ID.
func (s *Service) GetUser(ctx context.Context, id string) (*domain.User, error) {
	return s.store.GetUserByID(ctx, id)
}

// ListOrganizations returns the organizations a user belongs to.
func (s *Service) ListOrganizations(ctx context.Context, userID string) ([]domain.Organization, error) {
	return s.store.ListOrganizationsForUser(ctx, userID)
}

// CreateOrganization creates an organization owned by the given user.
func (s *Service) CreateOrganization(ctx context.Context, userID, name string) (*domain.Organization, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("%w: organization name is required", ErrValidation)
	}
	org := &domain.Organization{
		ID:        s.idgen(),
		Name:      name,
		Slug:      slugify(name) + "-" + shortID(s.idgen()),
		CreatedAt: s.now(),
	}
	if err := s.store.CreateOrganization(ctx, org); err != nil {
		return nil, err
	}
	if err := s.store.AddMembership(ctx, domain.Membership{OrgID: org.ID, UserID: userID, Role: domain.RoleOwner}); err != nil {
		return nil, err
	}
	if _, err := s.createDefaultProject(ctx, org.ID); err != nil {
		return nil, err
	}
	return org, nil
}

// Authorize reports an error unless the user is a member of the organization with
// at least the required role.
func (s *Service) Authorize(ctx context.Context, userID, orgID string, min domain.Role) (*domain.Membership, error) {
	m, err := s.store.GetMembership(ctx, orgID, userID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrForbidden
	}
	if err != nil {
		return nil, err
	}
	if !m.Role.AtLeast(min) {
		return nil, ErrForbidden
	}
	return m, nil
}

// ---- Projects (Org → Project → App) ----

func (s *Service) createDefaultProject(ctx context.Context, orgID string) (*domain.Project, error) {
	p := &domain.Project{
		ID:        s.idgen(),
		OrgID:     orgID,
		Name:      "Default",
		Slug:      "default",
		IsDefault: true,
		CreatedAt: s.now(),
	}
	if err := s.store.CreateProject(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}

// DefaultProject returns the org's default project (or its first project).
func (s *Service) DefaultProject(ctx context.Context, orgID string) (*domain.Project, error) {
	projects, err := s.store.ListProjectsByOrg(ctx, orgID)
	if err != nil {
		return nil, err
	}
	for i := range projects {
		if projects[i].IsDefault {
			return &projects[i], nil
		}
	}
	if len(projects) > 0 {
		return &projects[0], nil
	}
	return nil, store.ErrNotFound
}

// CreateProject creates a project in the org (caller must be org admin+).
func (s *Service) CreateProject(ctx context.Context, userID, orgID, name string) (*domain.Project, error) {
	if _, err := s.Authorize(ctx, userID, orgID, domain.RoleAdmin); err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("%w: project name is required", ErrValidation)
	}
	p := &domain.Project{
		ID:        s.idgen(),
		OrgID:     orgID,
		Name:      name,
		Slug:      slugify(name) + "-" + shortID(s.idgen()),
		CreatedAt: s.now(),
	}
	if err := s.store.CreateProject(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}

// ListProjects returns the projects in an org (caller must be a member).
func (s *Service) ListProjects(ctx context.Context, userID, orgID string) ([]domain.Project, error) {
	if _, err := s.Authorize(ctx, userID, orgID, domain.RoleMember); err != nil {
		return nil, err
	}
	return s.store.ListProjectsByOrg(ctx, orgID)
}

// AuthorizeProject reports an error unless the user can act on the project with
// at least the required role. Org admins/owners have full access to every
// project; otherwise the user must have a project membership of sufficient role.
func (s *Service) AuthorizeProject(ctx context.Context, userID, orgID, projectID string, min domain.Role) error {
	p, err := s.store.GetProject(ctx, projectID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && p.OrgID != orgID) {
		return ErrForbidden
	}
	if err != nil {
		return err
	}
	if m, err := s.store.GetMembership(ctx, orgID, userID); err == nil && m.Role.AtLeast(domain.RoleAdmin) {
		return nil
	}
	pm, err := s.store.GetProjectMembership(ctx, projectID, userID)
	if errors.Is(err, store.ErrNotFound) {
		return ErrForbidden
	}
	if err != nil {
		return err
	}
	if !pm.Role.AtLeast(min) {
		return ErrForbidden
	}
	return nil
}

// ---- Invitations (invite to an org, or to a specific project) ----

// ListMembers returns the org's memberships (caller must be a member).
func (s *Service) ListMembers(ctx context.Context, userID, orgID string) ([]domain.Membership, error) {
	if _, err := s.Authorize(ctx, userID, orgID, domain.RoleMember); err != nil {
		return nil, err
	}
	return s.store.ListMemberships(ctx, orgID)
}

// Invite creates an invitation to the org, or to a specific project when
// projectID is set. Caller must be an org admin+. Returns the invitation
// (its Token is the accept credential).
func (s *Service) Invite(ctx context.Context, inviterID, orgID, projectID, email string, role domain.Role) (*domain.Invitation, error) {
	if _, err := s.Authorize(ctx, inviterID, orgID, domain.RoleAdmin); err != nil {
		return nil, err
	}
	email = normalizeEmail(email)
	if !emailRe.MatchString(email) {
		return nil, fmt.Errorf("%w: invalid email", ErrValidation)
	}
	if !role.Valid() {
		return nil, fmt.Errorf("%w: invalid role", ErrValidation)
	}
	if projectID != "" {
		p, err := s.store.GetProject(ctx, projectID)
		if err != nil || p.OrgID != orgID {
			return nil, fmt.Errorf("%w: project not in organization", ErrValidation)
		}
	}
	inv := &domain.Invitation{
		ID:        s.idgen(),
		OrgID:     orgID,
		ProjectID: projectID,
		Email:     email,
		Role:      role,
		Token:     newToken(),
		Status:    domain.InvitePending,
		InvitedBy: inviterID,
		CreatedAt: s.now(),
	}
	if err := s.store.CreateInvitation(ctx, inv); err != nil {
		return nil, err
	}
	return inv, nil
}

// ListInvitations returns an org's invitations (caller must be org admin+).
func (s *Service) ListInvitations(ctx context.Context, userID, orgID string) ([]domain.Invitation, error) {
	if _, err := s.Authorize(ctx, userID, orgID, domain.RoleAdmin); err != nil {
		return nil, err
	}
	return s.store.ListInvitationsByOrg(ctx, orgID)
}

// AcceptInvitation accepts a pending invitation for the authenticated user. The
// user's email must match the invitation. An org invite grants org membership;
// a project invite grants project membership (plus baseline org membership so
// the user can navigate the org).
func (s *Service) AcceptInvitation(ctx context.Context, userID, userEmail, token string) (*domain.Invitation, error) {
	inv, err := s.store.GetInvitationByToken(ctx, token)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrInvitationInvalid
	}
	if err != nil {
		return nil, err
	}
	if inv.Status != domain.InvitePending {
		return nil, ErrInvitationInvalid
	}
	if normalizeEmail(userEmail) != inv.Email {
		return nil, ErrForbidden
	}

	// Ensure a baseline org membership exists.
	if _, err := s.store.GetMembership(ctx, inv.OrgID, userID); errors.Is(err, store.ErrNotFound) {
		role := domain.RoleMember
		if inv.ProjectID == "" {
			role = inv.Role // org-level invite carries the granted role
		}
		if err := s.store.AddMembership(ctx, domain.Membership{OrgID: inv.OrgID, UserID: userID, Role: role}); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}

	if inv.ProjectID != "" {
		err := s.store.AddProjectMembership(ctx, domain.ProjectMembership{ProjectID: inv.ProjectID, UserID: userID, Role: inv.Role})
		if err != nil && !errors.Is(err, store.ErrConflict) {
			return nil, err
		}
	}

	inv.Status = domain.InviteAccepted
	if err := s.store.UpdateInvitation(ctx, inv); err != nil {
		return nil, err
	}
	return inv, nil
}

func newToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failure is catastrophic; fall back to a UUID-derived token.
		return strings.ReplaceAll(uuid.NewString()+uuid.NewString(), "-", "")
	}
	return hex.EncodeToString(b)
}

func (s *Service) issue(user *domain.User) (*AuthResult, error) {
	access, err := s.tokens.Issue(user.ID, auth.AccessToken)
	if err != nil {
		return nil, err
	}
	refresh, err := s.tokens.Issue(user.ID, auth.RefreshToken)
	if err != nil {
		return nil, err
	}
	return &AuthResult{User: user, Access: access, Refresh: refresh}, nil
}

func normalizeEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

func personalOrgName(name string) string {
	if name == "" {
		return "Personal"
	}
	return name + "'s Org"
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = nonSlug.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "org"
	}
	return s
}

func shortID(id string) string {
	id = strings.ReplaceAll(id, "-", "")
	if len(id) > 6 {
		return id[:6]
	}
	return id
}
