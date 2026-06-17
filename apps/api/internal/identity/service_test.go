package identity

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/auth"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/store"
)

func newService() *Service {
	s := store.NewMemoryStore()
	tm := auth.NewTokenManager("test-secret", 15*time.Minute, time.Hour)
	return NewService(s, tm, nil)
}

func TestSignupCreatesUserOrgAndOwnerMembership(t *testing.T) {
	svc := newService()
	res, err := svc.Signup(context.Background(), "Alice@Example.com", "Alice", "supersecret")
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	if res.User.Email != "alice@example.com" {
		t.Fatalf("email not normalized: %q", res.User.Email)
	}
	if res.Access == "" || res.Refresh == "" {
		t.Fatal("expected access and refresh tokens")
	}
	orgs, err := svc.ListOrganizations(context.Background(), res.User.ID)
	if err != nil {
		t.Fatalf("list orgs: %v", err)
	}
	if len(orgs) != 1 {
		t.Fatalf("expected 1 personal org, got %d", len(orgs))
	}
	if _, err := svc.Authorize(context.Background(), res.User.ID, orgs[0].ID, domain.RoleOwner); err != nil {
		t.Fatalf("expected owner of personal org: %v", err)
	}
}

func TestSignupRejectsDuplicateEmail(t *testing.T) {
	svc := newService()
	ctx := context.Background()
	if _, err := svc.Signup(ctx, "bob@example.com", "Bob", "supersecret"); err != nil {
		t.Fatalf("first signup: %v", err)
	}
	_, err := svc.Signup(ctx, "BOB@example.com", "Bob Again", "supersecret")
	if !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("expected ErrEmailTaken, got %v", err)
	}
}

func TestSignupValidatesPassword(t *testing.T) {
	svc := newService()
	_, err := svc.Signup(context.Background(), "c@example.com", "C", "short")
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation, got %v", err)
	}
}

func TestLogin(t *testing.T) {
	svc := newService()
	ctx := context.Background()
	if _, err := svc.Signup(ctx, "dora@example.com", "Dora", "supersecret"); err != nil {
		t.Fatalf("signup: %v", err)
	}
	if _, err := svc.Login(ctx, "dora@example.com", "supersecret"); err != nil {
		t.Fatalf("login: %v", err)
	}
	if _, err := svc.Login(ctx, "dora@example.com", "wrong"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
	if _, err := svc.Login(ctx, "nobody@example.com", "supersecret"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials for unknown user, got %v", err)
	}
}

func TestRefresh(t *testing.T) {
	svc := newService()
	ctx := context.Background()
	res, err := svc.Signup(ctx, "ed@example.com", "Ed", "supersecret")
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	refreshed, err := svc.Refresh(ctx, res.Refresh)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if refreshed.User.ID != res.User.ID {
		t.Fatalf("refresh returned different user")
	}
	// An access token must not be accepted as a refresh token.
	if _, err := svc.Refresh(ctx, res.Access); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected access token to be rejected for refresh, got %v", err)
	}
}

// TestRefreshRotationRevokesOldToken asserts refresh-token rotation: after a
// successful refresh the OLD refresh token is revoked and rejected (401),
// while the freshly issued one works.
func TestRefreshRotationRevokesOldToken(t *testing.T) {
	svc := newService()
	ctx := context.Background()
	res, err := svc.Signup(ctx, "rot@example.com", "Rot", "supersecret")
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	rotated, err := svc.Refresh(ctx, res.Refresh)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	// Reusing the original refresh token after rotation must fail.
	if _, err := svc.Refresh(ctx, res.Refresh); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected reused refresh token to be rejected, got %v", err)
	}
	// The newly issued refresh token works.
	if _, err := svc.Refresh(ctx, rotated.Refresh); err != nil {
		t.Fatalf("rotated refresh token should work: %v", err)
	}
}

// TestRefreshUnknownJTIRejected asserts a structurally-valid refresh token whose
// jti has no stored record (e.g. issued by a prior key or never persisted) is
// rejected.
func TestRefreshUnknownJTIRejected(t *testing.T) {
	svc := newService()
	ctx := context.Background()
	res, err := svc.Signup(ctx, "unknown@example.com", "U", "supersecret")
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	// Issue a refresh token directly via the token manager (no stored record).
	orphan, err := svc.tokens.Issue(res.User.ID, auth.RefreshToken)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := svc.Refresh(ctx, orphan); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected unknown-jti refresh to be rejected, got %v", err)
	}
}

// TestLogoutRevokesRefreshToken asserts logout revokes the caller's refresh
// token so it can no longer be used.
func TestLogoutRevokesRefreshToken(t *testing.T) {
	svc := newService()
	ctx := context.Background()
	res, err := svc.Signup(ctx, "logout@example.com", "L", "supersecret")
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	if err := svc.Logout(ctx, res.Refresh); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if _, err := svc.Refresh(ctx, res.Refresh); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected refresh after logout to fail, got %v", err)
	}
	// Logout is idempotent / tolerant of empty or invalid tokens.
	if err := svc.Logout(ctx, ""); err != nil {
		t.Fatalf("logout empty: %v", err)
	}
}

func TestAuthorizeRequiresMembership(t *testing.T) {
	svc := newService()
	ctx := context.Background()
	owner, _ := svc.Signup(ctx, "owner@example.com", "Owner", "supersecret")
	outsider, _ := svc.Signup(ctx, "outsider@example.com", "Out", "supersecret")
	orgs, _ := svc.ListOrganizations(ctx, owner.User.ID)
	orgID := orgs[0].ID

	if _, err := svc.Authorize(ctx, outsider.User.ID, orgID, domain.RoleMember); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected outsider to be forbidden, got %v", err)
	}
}

func TestSignupRejectsInvalidEmail(t *testing.T) {
	svc := newService()
	for _, email := range []string{"not-an-email", "no-at-sign.com", "@example.com", ""} {
		if _, err := svc.Signup(context.Background(), email, "X", "supersecret"); !errors.Is(err, ErrValidation) {
			t.Fatalf("email %q: expected ErrValidation, got %v", email, err)
		}
	}
}

func TestSignupRejectsEmptyPassword(t *testing.T) {
	svc := newService()
	if _, err := svc.Signup(context.Background(), "ep@example.com", "EP", ""); !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation for empty password, got %v", err)
	}
}

func TestSignupRejectsPasswordOver72Bytes(t *testing.T) {
	svc := newService()
	long := strings.Repeat("a", 73)
	if _, err := svc.Signup(context.Background(), "lp@example.com", "LP", long); !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation for >72-byte password, got %v", err)
	}
}
