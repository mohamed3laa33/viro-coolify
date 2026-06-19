// Package identity implements signup/login/refresh and organization management.
package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/auth"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/notify"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/store"
)

// Errors returned by the identity service.
var (
	ErrEmailTaken         = errors.New("identity: email already registered")
	ErrInvalidCredentials = errors.New("identity: invalid email or password")
	ErrValidation         = errors.New("identity: validation failed")
	ErrForbidden          = errors.New("identity: forbidden")
	ErrInvitationInvalid  = errors.New("identity: invitation is invalid or expired")
	ErrNotFound           = errors.New("identity: not found")
	// ErrConflict reports a state conflict that must not proceed, e.g. removing
	// or demoting the last remaining owner of an organization, or deleting a
	// project that still owns apps/services.
	ErrConflict = errors.New("identity: conflict")
)

// OrgProvisioner provisions the per-org wildcard TLS certificate and matching
// shared-Gateway listener(s) for a newly created organization, so the generated
// tenant host <app>.<project>.<org>.<baseDomain> terminates TLS and routes. It is
// the seam onto the Kubernetes deploy backend (kube.Backend implements it); the
// identity package depends on this small interface rather than the concrete
// backend so it stays testable without a cluster.
//
// projectSlugs are the org's known project slugs at call time (the default project
// always exists at org creation). Implementations MUST be idempotent and tolerate
// the resources already existing (re-creating an org / re-running provisioning is
// safe).
type OrgProvisioner interface {
	EnsureOrgWildcard(ctx context.Context, orgSlug string, projectSlugs []string) error
}

// noopOrgProvisioner is the default OrgProvisioner: it provisions nothing. It keeps
// the service usable without a Kubernetes backend (unit tests, dev) — it is an
// HONEST no-op (it does not pretend a cert was issued), not a fake-success path.
type noopOrgProvisioner struct{}

func (noopOrgProvisioner) EnsureOrgWildcard(context.Context, string, []string) error { return nil }

// Service holds identity business logic.
type Service struct {
	store       store.Store
	tokens      *auth.TokenManager
	adminEmails map[string]bool
	idgen       func() string
	now         func() time.Time

	// mailer delivers transactional email (invitations, welcome, password reset).
	// Never nil after NewService (defaults to a NoopMailer).
	mailer notify.Mailer
	logger *slog.Logger
	// orgProvisioner provisions the per-org wildcard TLS certificate + shared-Gateway
	// listeners so a new org's tenant hosts (<app>.<project>.<org>.<baseDomain>)
	// terminate TLS on first use. Never nil after NewService (defaults to a no-op so
	// the service is usable without a Kubernetes backend, e.g. in unit tests).
	orgProvisioner OrgProvisioner
	// baseDomain is the platform apex used to build accept/reset links.
	baseDomain string
	// refreshTTL bounds a stored refresh token's lifetime (mirrors the JWT exp);
	// used to set expires_at on issue. Zero leaves expires_at unset.
	refreshTTL time.Duration
	// invitationTTL bounds how long an invitation can be accepted after creation.
	invitationTTL time.Duration
	// passwordResetTTL bounds how long a reset token is valid after issue.
	passwordResetTTL time.Duration
}

// Option customizes Service construction (mailer, base domain, TTLs). Each is
// optional; unset values fall back to safe defaults (NoopMailer, no expiry).
type Option func(*Service)

// WithMailer injects the email transport. A nil mailer is treated as a NoopMailer
// so the service is always safe to call.
func WithMailer(m notify.Mailer) Option {
	return func(s *Service) {
		if m != nil {
			s.mailer = m
		}
	}
}

// WithLogger injects a logger for best-effort email failures.
func WithLogger(l *slog.Logger) Option {
	return func(s *Service) {
		if l != nil {
			s.logger = l
		}
	}
}

// WithBaseDomain sets the platform apex used to build accept/reset URLs.
func WithBaseDomain(d string) Option {
	return func(s *Service) { s.baseDomain = strings.TrimSpace(d) }
}

// WithOrgProvisioner injects the per-org wildcard TLS/Gateway provisioner (the
// Kubernetes deploy backend). A nil provisioner is treated as a no-op so the
// service is always safe to call.
func WithOrgProvisioner(p OrgProvisioner) Option {
	return func(s *Service) {
		if p != nil {
			s.orgProvisioner = p
		}
	}
}

// WithRefreshTTL sets the stored refresh-token lifetime used to stamp expires_at.
func WithRefreshTTL(d time.Duration) Option {
	return func(s *Service) {
		if d > 0 {
			s.refreshTTL = d
		}
	}
}

// WithInvitationTTL sets how long an invitation can be accepted after creation.
func WithInvitationTTL(d time.Duration) Option {
	return func(s *Service) {
		if d > 0 {
			s.invitationTTL = d
		}
	}
}

// WithPasswordResetTTL sets how long a reset token is valid after issue.
func WithPasswordResetTTL(d time.Duration) Option {
	return func(s *Service) {
		if d > 0 {
			s.passwordResetTTL = d
		}
	}
}

// NewService builds an identity service backed by the given store and token
// manager. adminEmails (normalized) are granted platform-wide super-admin.
func NewService(s store.Store, tm *auth.TokenManager, adminEmails []string, opts ...Option) *Service {
	admins := make(map[string]bool, len(adminEmails))
	for _, e := range adminEmails {
		admins[normalizeEmail(e)] = true
	}
	svc := &Service{
		store:            s,
		tokens:           tm,
		adminEmails:      admins,
		idgen:            uuid.NewString,
		now:              time.Now,
		mailer:           notify.NewNoopMailer(),
		logger:           slog.Default(),
		orgProvisioner:   noopOrgProvisioner{},
		invitationTTL:    7 * 24 * time.Hour,
		passwordResetTTL: time.Hour,
	}
	for _, opt := range opts {
		opt(svc)
	}
	return svc
}

// isAdminEmail reports whether the normalized email is a configured super-admin.
func (s *Service) isAdminEmail(email string) bool { return s.adminEmails[normalizeEmail(email)] }

// AuthResult is the outcome of a successful authentication.
type AuthResult struct {
	User    *domain.User
	Access  string
	Refresh string
}

var emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// decoyPasswordHash is a fixed, precomputed bcrypt hash of a dummy password. When
// Login is given an unknown email we still run a bcrypt compare against this decoy
// so the response time matches the known-email (real-hash) path, closing the timing
// oracle that would otherwise let an attacker enumerate registered emails. It is
// computed once at startup (bcrypt.DefaultCost) so the work mirrors a real compare.
var decoyPasswordHash = mustDecoyHash()

func mustDecoyHash() string {
	h, err := auth.HashPassword("viro-login-timing-decoy-password")
	if err != nil {
		// HashPassword only fails on a broken crypto runtime; a panic at init is the
		// right signal since the whole auth subsystem would be unusable anyway.
		panic("identity: cannot precompute decoy password hash: " + err.Error())
	}
	return h
}

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
		IsAdmin:      s.isAdminEmail(email),
		CreatedAt:    s.now(),
	}
	org := &domain.Organization{
		ID:        s.idgen(),
		Name:      personalOrgName(name),
		Slug:      slugify(name) + "-" + shortID(user.ID),
		CreatedAt: s.now(),
	}

	// Create the user, their personal org, owner membership and default project
	// ATOMICALLY: a mid-sequence failure must not orphan a user with no org (or an
	// org with no owner). Postgres rolls back; the memory store serializes.
	var defaultProjectSlug string
	if err := s.store.WithTx(ctx, func(tx store.Store) error {
		if err := tx.CreateUser(ctx, user); err != nil {
			if errors.Is(err, store.ErrConflict) {
				return ErrEmailTaken
			}
			return err
		}
		if err := tx.CreateOrganization(ctx, org); err != nil {
			return err
		}
		if err := tx.AddMembership(ctx, domain.Membership{OrgID: org.ID, UserID: user.ID, Role: domain.RoleOwner}); err != nil {
			return err
		}
		p, err := s.createDefaultProjectTx(ctx, tx, org.ID)
		if err != nil {
			return err
		}
		defaultProjectSlug = p.Slug
		return nil
	}); err != nil {
		return nil, err
	}

	// Provision the personal org's per-org wildcard TLS cert + Gateway listener
	// (best-effort, post-commit — see provisionOrgWildcard) so its tenant hosts
	// terminate TLS on first use.
	s.provisionOrgWildcard(ctx, org.Slug, defaultProjectSlug)

	// Welcome email is best-effort: it must never fail signup.
	s.sendBestEffort(ctx, user.Email, notify.WelcomeEmail(user.Name))

	return s.issue(ctx, user)
}

// Login authenticates by email + password.
func (s *Service) Login(ctx context.Context, email, password string) (*AuthResult, error) {
	user, err := s.store.GetUserByEmail(ctx, normalizeEmail(email))
	if errors.Is(err, store.ErrNotFound) {
		// Run bcrypt against a fixed decoy hash so an unknown email costs the same
		// wall-clock time as a known one — without this, the fast no-hash path is a
		// timing oracle for enumerating registered emails. The result is discarded;
		// the outcome is always the uniform ErrInvalidCredentials.
		_ = auth.CheckPassword(decoyPasswordHash, password)
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}
	if !auth.CheckPassword(user.PasswordHash, password) {
		return nil, ErrInvalidCredentials
	}
	// Reconcile super-admin status against the current admin list.
	if want := s.isAdminEmail(user.Email); want != user.IsAdmin {
		user.IsAdmin = want
		if err := s.store.UpdateUser(ctx, user); err != nil {
			return nil, err
		}
	}
	return s.issue(ctx, user)
}

// Refresh exchanges a valid refresh token for a fresh token pair, rotating the
// refresh token: the presented token's jti must reference a stored, non-revoked
// record; on success the old record is revoked and a new pair issued (with a new
// stored record). Reuse of a revoked or unknown jti is rejected as invalid
// credentials (a 401 at the HTTP layer).
func (s *Service) Refresh(ctx context.Context, refreshToken string) (*AuthResult, error) {
	claims, err := s.tokens.Verify(refreshToken, auth.RefreshToken)
	if err != nil {
		return nil, ErrInvalidCredentials
	}
	if claims.ID == "" {
		return nil, ErrInvalidCredentials
	}
	rec, err := s.store.GetRefreshToken(ctx, claims.ID)
	if err != nil {
		return nil, ErrInvalidCredentials
	}
	// REUSE DETECTION: a verified token whose stored record is already revoked is a
	// replay of a rotated token (the legitimate holder already rotated it away).
	// Treat it as a stolen-token signal and kill the whole family so an attacker
	// who captured one rotated token cannot keep any session alive.
	if rec.Revoked {
		_ = s.store.RevokeAllUserRefreshTokens(ctx, rec.UserID)
		return nil, ErrInvalidCredentials
	}
	// Reject an expired stored token (the JWT exp is also checked by Verify, but the
	// stored expiry is the authoritative server-side bound).
	if !rec.ExpiresAt.IsZero() && !s.now().Before(rec.ExpiresAt) {
		return nil, ErrInvalidCredentials
	}
	user, err := s.store.GetUserByID(ctx, claims.Subject)
	if err != nil {
		return nil, ErrInvalidCredentials
	}
	// ATOMIC ROTATION: revoke the presented token only if it is still active. If
	// the compare-and-set loses (revoked==false), a concurrent rotation already
	// consumed this token; treat the lost race as invalid so exactly one new
	// session is minted from a single presented token.
	revoked, err := s.store.RevokeRefreshTokenIfActive(ctx, claims.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}
	if !revoked {
		// Lost the race to a concurrent rotation, OR a replay slipped past the read
		// above. Kill the family to be safe and reject.
		_ = s.store.RevokeAllUserRefreshTokens(ctx, rec.UserID)
		return nil, ErrInvalidCredentials
	}
	return s.issue(ctx, user)
}

// Logout revokes the refresh token identified by the given refresh-token string
// (typically read from the caller's cookie). A missing/invalid token is not an
// error — logout is idempotent and best-effort.
func (s *Service) Logout(ctx context.Context, refreshToken string) error {
	if refreshToken == "" {
		return nil
	}
	// A malformed/expired token (or one without a jti) has nothing to revoke;
	// logout stays best-effort and succeeds so the caller's cookies are cleared.
	claims, verifyErr := s.tokens.Verify(refreshToken, auth.RefreshToken)
	if verifyErr == nil && claims.ID != "" {
		if err := s.store.RevokeRefreshToken(ctx, claims.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
	}
	return nil
}

// GetUser returns a user by ID.
func (s *Service) GetUser(ctx context.Context, id string) (*domain.User, error) {
	return s.store.GetUserByID(ctx, id)
}

// ForgotPassword starts the password-reset flow. It is ENUMERATION-SAFE: it always
// returns nil regardless of whether the email exists, doing real work (creating a
// single-use, time-limited, hashed-at-rest reset token and emailing the link) only
// when a matching user exists. Email delivery is best-effort.
func (s *Service) ForgotPassword(ctx context.Context, email string) error {
	email = normalizeEmail(email)
	if !emailRe.MatchString(email) {
		// Don't leak validity via a 400 either — silently succeed.
		return nil
	}
	user, err := s.store.GetUserByEmail(ctx, email)
	if errors.Is(err, store.ErrNotFound) {
		return nil // no enumeration: same outcome as a real send
	}
	if err != nil {
		return err
	}

	plaintext := newToken()
	now := s.now()
	rec := &domain.PasswordResetToken{
		ID:        s.idgen(),
		UserID:    user.ID,
		TokenHash: hashToken(plaintext),
		ExpiresAt: now.Add(s.passwordResetTTL),
		CreatedAt: now,
	}
	// Invalidate any prior unused reset tokens and create the new one ATOMICALLY, so
	// only the most-recently-issued reset link is ever live (a re-requested reset
	// must not leave the previous link usable).
	if err := s.store.WithTx(ctx, func(tx store.Store) error {
		if err := tx.InvalidateUserPasswordResetTokens(ctx, user.ID, now); err != nil {
			return err
		}
		return tx.CreatePasswordResetToken(ctx, rec)
	}); err != nil {
		// Log but stay enumeration-safe: still return nil so the caller 204s.
		s.logger.Error("identity: create password reset token", "err", err)
		return nil
	}

	msg := notify.PasswordResetEmail(user.Name, s.resetURL(plaintext))
	msg.To = user.Email
	s.sendBestEffort(ctx, user.Email, msg)
	return nil
}

// ResetPassword completes the reset flow: it validates the token (unexpired,
// unused), sets the new bcrypt password hash, consumes the token (single-use), and
// revokes ALL the user's refresh tokens so any session opened with the old
// password is killed. An expired/used/unknown token yields ErrInvalidCredentials.
func (s *Service) ResetPassword(ctx context.Context, token, newPassword string) error {
	if len(newPassword) < 8 {
		return fmt.Errorf("%w: password must be at least 8 characters", ErrValidation)
	}
	if len(newPassword) > 72 {
		return fmt.Errorf("%w: password must be at most 72 bytes", ErrValidation)
	}
	rec, err := s.store.GetPasswordResetTokenByHash(ctx, hashToken(token))
	if errors.Is(err, store.ErrNotFound) {
		return ErrInvalidCredentials
	}
	if err != nil {
		return err
	}
	if !rec.UsedAt.IsZero() {
		return ErrInvalidCredentials // already consumed
	}
	if !s.now().Before(rec.ExpiresAt) {
		return ErrInvalidCredentials // expired
	}

	user, err := s.store.GetUserByID(ctx, rec.UserID)
	if err != nil {
		return ErrInvalidCredentials
	}
	hash, err := auth.HashPassword(newPassword)
	if err != nil {
		return err
	}

	user.PasswordHash = hash
	// Consume the token, set the new password and revoke all sessions ATOMICALLY: a
	// crash between consume and update must not burn a single-use token without
	// actually resetting the password (which would lock the user out of recovery).
	// Consuming FIRST inside the tx still gives single-use semantics — a concurrent
	// request that already consumed the token makes this one a no-op (consumed=false)
	// and rolls back. Postgres rolls back on error; the memory store serializes.
	var consumed bool
	if err := s.store.WithTx(ctx, func(tx store.Store) error {
		ok, cErr := tx.ConsumePasswordResetToken(ctx, rec.ID, s.now())
		if cErr != nil {
			return cErr
		}
		if !ok {
			consumed = false
			return nil // already consumed (replay): leave password unchanged
		}
		consumed = true
		if uErr := tx.UpdateUser(ctx, user); uErr != nil {
			return uErr
		}
		// Kill every existing session: a password reset implies the account may have
		// been compromised, so all refresh tokens are revoked.
		return tx.RevokeAllUserRefreshTokens(ctx, user.ID)
	}); err != nil {
		return err
	}
	if !consumed {
		return ErrInvalidCredentials
	}
	return nil
}

// hashToken returns the lowercase hex SHA-256 of a token. Reset tokens are stored
// hashed at rest so a database leak does not yield usable plaintext tokens.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// CleanupExpiredRefreshTokens deletes expired/revoked refresh-token rows, returning
// the number removed. It is invoked by the background cleanup ticker.
func (s *Service) CleanupExpiredRefreshTokens(ctx context.Context) (int64, error) {
	return s.store.DeleteExpiredRefreshTokens(ctx, s.now())
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
	// Org + owner membership + default project are created atomically so a failure
	// can never leave an ownerless org.
	var defaultProjectSlug string
	if err := s.store.WithTx(ctx, func(tx store.Store) error {
		if err := tx.CreateOrganization(ctx, org); err != nil {
			return err
		}
		if err := tx.AddMembership(ctx, domain.Membership{OrgID: org.ID, UserID: userID, Role: domain.RoleOwner}); err != nil {
			return err
		}
		p, err := s.createDefaultProjectTx(ctx, tx, org.ID)
		if err != nil {
			return err
		}
		defaultProjectSlug = p.Slug
		return nil
	}); err != nil {
		return nil, err
	}
	// Provision the per-org wildcard TLS cert + Gateway listener AFTER the org is
	// durably committed. It is a best-effort external side effect: a failure is
	// logged but must NOT orphan/roll back the org. Provisioning is idempotent, so
	// re-running it (operator retry) safely reconciles. See provisionOrgWildcard.
	s.provisionOrgWildcard(ctx, org.Slug, defaultProjectSlug)
	return org, nil
}

// provisionOrgWildcard requests the per-org wildcard TLS certificate + matching
// shared-Gateway listener(s) so the org's generated tenant hosts
// (<app>.<project>.<org>.<baseDomain>) terminate TLS and route on first use.
//
// It is best-effort by design: the org has already been committed, so a transient
// cluster/cert-manager failure here must never fail org creation or leave the org
// half-provisioned. The error is LOGGED (the actionable signal); provisioning is
// idempotent and is retried implicitly when the org's first app deploys. With no
// backend wired (unit tests / dev) the default no-op provisioner makes this a
// silent success.
func (s *Service) provisionOrgWildcard(ctx context.Context, orgSlug string, projectSlugs ...string) {
	slugs := make([]string, 0, len(projectSlugs))
	for _, p := range projectSlugs {
		if p = strings.TrimSpace(p); p != "" {
			slugs = append(slugs, p)
		}
	}
	if err := s.orgProvisioner.EnsureOrgWildcard(ctx, orgSlug, slugs); err != nil {
		s.logger.Error("identity: provision per-org wildcard TLS/Gateway",
			"org", orgSlug, "err", err)
	}
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

// UpdateOrgInput carries the editable organization fields. A nil pointer leaves
// the corresponding field unchanged.
type UpdateOrgInput struct {
	Name         *string
	BillingEmail *string
}

// UpdateOrganization mutates the org's editable fields (name, billing email).
// Caller must be an org admin+. Empty/whitespace-only names are rejected; a
// supplied billing email must be a valid address (an empty string clears it).
func (s *Service) UpdateOrganization(ctx context.Context, userID, orgID string, in UpdateOrgInput) (*domain.Organization, error) {
	if _, err := s.Authorize(ctx, userID, orgID, domain.RoleAdmin); err != nil {
		return nil, err
	}
	org, err := s.store.GetOrganization(ctx, orgID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get organization: %w", err)
	}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			return nil, fmt.Errorf("%w: organization name is required", ErrValidation)
		}
		org.Name = name
	}
	if in.BillingEmail != nil {
		email := normalizeEmail(*in.BillingEmail)
		if email != "" && !emailRe.MatchString(email) {
			return nil, fmt.Errorf("%w: invalid billing email", ErrValidation)
		}
		org.BillingEmail = email
	}
	if err := s.store.UpdateOrg(ctx, org); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("update organization: %w", err)
	}
	return org, nil
}

// UpdateMemberRole changes an existing member's role within an org. Caller must
// be an org owner. Demoting the last remaining owner is rejected with
// ErrConflict so an org is never left ownerless.
func (s *Service) UpdateMemberRole(ctx context.Context, actorID, orgID, targetUserID string, role domain.Role) error {
	if _, err := s.Authorize(ctx, actorID, orgID, domain.RoleOwner); err != nil {
		return err
	}
	if !role.Valid() {
		return fmt.Errorf("%w: invalid role", ErrValidation)
	}
	target, err := s.store.GetMembership(ctx, orgID, targetUserID)
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("get membership: %w", err)
	}
	// Block demoting the org's last owner away from owner.
	if target.Role == domain.RoleOwner && role != domain.RoleOwner {
		owners, err := s.countOwners(ctx, orgID)
		if err != nil {
			return err
		}
		if owners <= 1 {
			return fmt.Errorf("%w: an organization must have at least one owner", ErrConflict)
		}
	}
	if err := s.store.UpdateMembershipRole(ctx, orgID, targetUserID, role); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotFound
		}
		return fmt.Errorf("update membership role: %w", err)
	}
	return nil
}

// RemoveMember removes a member from an org. Caller must be an org owner.
// Removing the last remaining owner is rejected with ErrConflict.
func (s *Service) RemoveMember(ctx context.Context, actorID, orgID, targetUserID string) error {
	if _, err := s.Authorize(ctx, actorID, orgID, domain.RoleOwner); err != nil {
		return err
	}
	target, err := s.store.GetMembership(ctx, orgID, targetUserID)
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("get membership: %w", err)
	}
	if target.Role == domain.RoleOwner {
		owners, err := s.countOwners(ctx, orgID)
		if err != nil {
			return err
		}
		if owners <= 1 {
			return fmt.Errorf("%w: an organization must have at least one owner", ErrConflict)
		}
	}
	if err := s.store.RemoveMembership(ctx, orgID, targetUserID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotFound
		}
		return fmt.Errorf("remove membership: %w", err)
	}
	return nil
}

// countOwners returns how many members of the org hold the owner role.
func (s *Service) countOwners(ctx context.Context, orgID string) (int, error) {
	members, err := s.store.ListMemberships(ctx, orgID)
	if err != nil {
		return 0, fmt.Errorf("list memberships: %w", err)
	}
	n := 0
	for _, m := range members {
		if m.Role == domain.RoleOwner {
			n++
		}
	}
	return n, nil
}

// ---- Projects (Org → Project → App) ----

// createDefaultProjectTx creates the org's default project against the given
// store (the live store, or a transaction-scoped store inside WithTx).
func (s *Service) createDefaultProjectTx(ctx context.Context, st store.Store, orgID string) (*domain.Project, error) {
	p := &domain.Project{
		ID:        s.idgen(),
		OrgID:     orgID,
		Name:      "Default",
		Slug:      "default",
		IsDefault: true,
		CreatedAt: s.now(),
	}
	if err := st.CreateProject(ctx, p); err != nil {
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

// DeleteProject removes an empty project from the org. Caller must be an org
// admin+. The default project cannot be deleted, and a project that still owns
// apps or services is rejected with ErrConflict (the store enforces emptiness).
func (s *Service) DeleteProject(ctx context.Context, userID, orgID, projectID string) error {
	if _, err := s.Authorize(ctx, userID, orgID, domain.RoleAdmin); err != nil {
		return err
	}
	p, err := s.store.GetProject(ctx, projectID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && p.OrgID != orgID) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("get project: %w", err)
	}
	if p.IsDefault {
		return fmt.Errorf("%w: the default project cannot be deleted", ErrConflict)
	}
	if err := s.store.DeleteProject(ctx, orgID, projectID); err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			return ErrNotFound
		case errors.Is(err, store.ErrConflict):
			return fmt.Errorf("%w: project still contains apps or services", ErrConflict)
		default:
			return fmt.Errorf("delete project: %w", err)
		}
	}
	return nil
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

// AccessibleProjectIDs returns the set of project ids in the org the user may
// access: ALL projects when the user is an org admin/owner, otherwise only the
// projects the user has a direct project membership in. The bool isOrgAdmin lets
// callers fast-path "see everything" without consulting the set. A user with no
// org membership gets an empty set (and isOrgAdmin=false).
func (s *Service) AccessibleProjectIDs(ctx context.Context, userID, orgID string) (ids map[string]bool, isOrgAdmin bool, err error) {
	ids = map[string]bool{}
	if m, mErr := s.store.GetMembership(ctx, orgID, userID); mErr == nil && m.Role.AtLeast(domain.RoleAdmin) {
		return ids, true, nil
	} else if mErr != nil && !errors.Is(mErr, store.ErrNotFound) {
		return nil, false, mErr
	}
	projects, err := s.store.ListProjectsByOrg(ctx, orgID)
	if err != nil {
		return nil, false, err
	}
	for i := range projects {
		if _, pErr := s.store.GetProjectMembership(ctx, projects[i].ID, userID); pErr == nil {
			ids[projects[i].ID] = true
		} else if !errors.Is(pErr, store.ErrNotFound) {
			return nil, false, pErr
		}
	}
	return ids, false, nil
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
	now := s.now()
	inv := &domain.Invitation{
		ID:        s.idgen(),
		OrgID:     orgID,
		ProjectID: projectID,
		Email:     email,
		Role:      role,
		Token:     newToken(),
		Status:    domain.InvitePending,
		InvitedBy: inviterID,
		CreatedAt: now,
	}
	if s.invitationTTL > 0 {
		inv.ExpiresAt = now.Add(s.invitationTTL)
	}
	if err := s.store.CreateInvitation(ctx, inv); err != nil {
		return nil, err
	}

	// Email the invitee an accept link (best-effort — a mail failure must never
	// fail the API or block the invitation, which is already persisted).
	s.sendInvitationEmail(ctx, inviterID, inv)

	return inv, nil
}

// sendInvitationEmail renders and sends the invitation email to the invitee. It
// resolves the inviter's display name and the org/project names for the body, and
// builds the accept URL from the configured base domain. Any failure is logged,
// not returned.
func (s *Service) sendInvitationEmail(ctx context.Context, inviterID string, inv *domain.Invitation) {
	inviterName := "A teammate"
	if u, err := s.store.GetUserByID(ctx, inviterID); err == nil && u != nil {
		if n := strings.TrimSpace(u.Name); n != "" {
			inviterName = n
		} else if u.Email != "" {
			inviterName = u.Email
		}
	}
	orgName := ""
	if o, err := s.store.GetOrganization(ctx, inv.OrgID); err == nil && o != nil {
		orgName = o.Name
	}
	projectName := ""
	if inv.ProjectID != "" {
		if p, err := s.store.GetProject(ctx, inv.ProjectID); err == nil && p != nil {
			projectName = p.Name
		}
	}
	msg := notify.InvitationEmail(inviterName, orgName, projectName, s.acceptURL(inv.Token))
	msg.To = inv.Email
	s.sendBestEffort(ctx, inv.Email, msg)
}

// acceptURL builds the invitation accept link from the configured base domain.
func (s *Service) acceptURL(token string) string {
	base := s.baseDomain
	if base == "" {
		base = "vortex.v60ai.com"
	}
	return "https://" + base + "/invite?token=" + url.QueryEscape(token)
}

// resetURL builds the password-reset link from the configured base domain.
func (s *Service) resetURL(token string) string {
	base := s.baseDomain
	if base == "" {
		base = "vortex.v60ai.com"
	}
	return "https://" + base + "/reset-password?token=" + url.QueryEscape(token)
}

// sendBestEffort delivers msg to addr, logging (never returning) any failure so
// email delivery can never fail or block the surrounding API call.
func (s *Service) sendBestEffort(ctx context.Context, addr string, msg notify.Message) {
	if msg.To == "" {
		msg.To = addr
	}
	if err := s.mailer.Send(ctx, msg); err != nil {
		s.logger.Warn("identity: email send failed", "to", addr, "subject", msg.Subject, "err", err)
	}
}

// ListInvitations returns an org's invitations (caller must be org admin+).
func (s *Service) ListInvitations(ctx context.Context, userID, orgID string) ([]domain.Invitation, error) {
	if _, err := s.Authorize(ctx, userID, orgID, domain.RoleAdmin); err != nil {
		return nil, err
	}
	return s.store.ListInvitationsByOrg(ctx, orgID)
}

// RevokeInvitation marks a pending invitation as revoked so its token can no
// longer be accepted. Caller must be an org admin+. A missing invitation (or one
// not belonging to the org) yields ErrNotFound.
func (s *Service) RevokeInvitation(ctx context.Context, userID, orgID, inviteID string) error {
	if _, err := s.Authorize(ctx, userID, orgID, domain.RoleAdmin); err != nil {
		return err
	}
	if err := s.store.RevokeInvitation(ctx, orgID, inviteID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotFound
		}
		return fmt.Errorf("revoke invitation: %w", err)
	}
	return nil
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
	// Reject an expired invitation (zero ExpiresAt = never expires, back-compat).
	if !inv.ExpiresAt.IsZero() && !s.now().Before(inv.ExpiresAt) {
		return nil, ErrInvitationInvalid
	}
	if normalizeEmail(userEmail) != inv.Email {
		return nil, ErrForbidden
	}

	// Grant membership(s) and mark the invitation accepted ATOMICALLY so a failure
	// mid-sequence cannot leave the user half-onboarded (e.g. a project membership
	// without the org membership, or an "accepted" invite with no membership).
	if err := s.store.WithTx(ctx, func(tx store.Store) error {
		// Ensure a baseline org membership exists.
		if _, err := tx.GetMembership(ctx, inv.OrgID, userID); errors.Is(err, store.ErrNotFound) {
			role := domain.RoleMember
			if inv.ProjectID == "" {
				role = inv.Role // org-level invite carries the granted role
			}
			if err := tx.AddMembership(ctx, domain.Membership{OrgID: inv.OrgID, UserID: userID, Role: role}); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}

		if inv.ProjectID != "" {
			err := tx.AddProjectMembership(ctx, domain.ProjectMembership{ProjectID: inv.ProjectID, UserID: userID, Role: inv.Role})
			if err != nil && !errors.Is(err, store.ErrConflict) {
				return err
			}
		}

		inv.Status = domain.InviteAccepted
		return tx.UpdateInvitation(ctx, inv)
	}); err != nil {
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

func (s *Service) issue(ctx context.Context, user *domain.User) (*AuthResult, error) {
	access, err := s.tokens.Issue(user.ID, auth.AccessToken)
	if err != nil {
		return nil, err
	}
	refresh, jti, err := s.tokens.IssueWithID(user.ID, auth.RefreshToken)
	if err != nil {
		return nil, err
	}
	// Persist the refresh token's jti so it can be rotated/revoked. Without a
	// matching, non-revoked record a refresh token is not honored. ExpiresAt
	// mirrors the JWT exp so the cleanup ticker can GC it and Refresh can reject an
	// expired stored token.
	now := s.now()
	rec := &domain.RefreshToken{ID: jti, UserID: user.ID, CreatedAt: now}
	if s.refreshTTL > 0 {
		rec.ExpiresAt = now.Add(s.refreshTTL)
	}
	if err := s.store.CreateRefreshToken(ctx, rec); err != nil {
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
