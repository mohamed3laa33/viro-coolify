package store

import (
	"context"
	"errors"
	"testing"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
)

func TestMemoryStoreUsers(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()

	u := &domain.User{ID: "u1", Email: "A@Example.com", Name: "A"}
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Email lookup is case-insensitive.
	got, err := s.GetUserByEmail(ctx, "a@example.com")
	if err != nil {
		t.Fatalf("get by email: %v", err)
	}
	if got.ID != "u1" {
		t.Fatalf("id = %q", got.ID)
	}
	// Duplicate email (different case) conflicts.
	if err := s.CreateUser(ctx, &domain.User{ID: "u2", Email: "a@EXAMPLE.com"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict, got %v", err)
	}
	if _, err := s.GetUserByID(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMemoryStoreMemberships(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	_ = s.CreateOrganization(ctx, &domain.Organization{ID: "o1", Name: "Org"})
	if err := s.AddMembership(ctx, domain.Membership{OrgID: "o1", UserID: "u1", Role: domain.RoleOwner}); err != nil {
		t.Fatalf("add membership: %v", err)
	}
	if err := s.AddMembership(ctx, domain.Membership{OrgID: "o1", UserID: "u1", Role: domain.RoleMember}); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict on duplicate membership, got %v", err)
	}
	orgs, err := s.ListOrganizationsForUser(ctx, "u1")
	if err != nil {
		t.Fatalf("list orgs: %v", err)
	}
	if len(orgs) != 1 || orgs[0].ID != "o1" {
		t.Fatalf("unexpected orgs: %+v", orgs)
	}
	members, err := s.ListMemberships(ctx, "o1")
	if err != nil {
		t.Fatalf("list memberships: %v", err)
	}
	if len(members) != 1 {
		t.Fatalf("expected 1 member, got %d", len(members))
	}
}
