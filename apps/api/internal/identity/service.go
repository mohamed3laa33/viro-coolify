// Package identity implements signup/login/refresh and organization management.
package identity

import (
	"context"
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
