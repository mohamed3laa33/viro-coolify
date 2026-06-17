package identity

import (
	"context"
	"errors"
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
