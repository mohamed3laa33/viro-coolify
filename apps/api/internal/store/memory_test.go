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

func TestMemoryStoreRefreshTokens(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()

	rt := &domain.RefreshToken{ID: "jti-1", UserID: "u1"}
	if err := s.CreateRefreshToken(ctx, rt); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Duplicate jti conflicts.
	if err := s.CreateRefreshToken(ctx, rt); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict, got %v", err)
	}
	got, err := s.GetRefreshToken(ctx, "jti-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Revoked {
		t.Fatal("new token should not be revoked")
	}
	// Unknown jti is not found.
	if _, err := s.GetRefreshToken(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	// Revoke then verify.
	if err := s.RevokeRefreshToken(ctx, "jti-1"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	got, _ = s.GetRefreshToken(ctx, "jti-1")
	if !got.Revoked {
		t.Fatal("token should be revoked")
	}
	// Revoking an unknown token is ErrNotFound.
	if err := s.RevokeRefreshToken(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	// RevokeAll revokes every live token for a user.
	_ = s.CreateRefreshToken(ctx, &domain.RefreshToken{ID: "jti-2", UserID: "u1"})
	_ = s.CreateRefreshToken(ctx, &domain.RefreshToken{ID: "jti-3", UserID: "u2"})
	if err := s.RevokeAllUserRefreshTokens(ctx, "u1"); err != nil {
		t.Fatalf("revoke all: %v", err)
	}
	if g, _ := s.GetRefreshToken(ctx, "jti-2"); !g.Revoked {
		t.Fatal("jti-2 should be revoked")
	}
	if g, _ := s.GetRefreshToken(ctx, "jti-3"); g.Revoked {
		t.Fatal("jti-3 (other user) should not be revoked")
	}
}

func TestMemoryStoreSeededDefaults(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()

	plans, err := s.ListPlans(ctx)
	if err != nil {
		t.Fatalf("list plans: %v", err)
	}
	if len(plans) != 3 {
		t.Fatalf("expected 3 seeded plans, got %d", len(plans))
	}
	hobby, err := s.GetPlan(ctx, "hobby")
	if err != nil {
		t.Fatalf("get hobby: %v", err)
	}
	if !hobby.IsDefault || !hobby.Active || hobby.MaxCPU != 0.5 || hobby.MaxMemoryMB != 512 || hobby.MaxApps != 3 {
		t.Fatalf("unexpected hobby plan: %+v", hobby)
	}

	tmpls, err := s.ListServiceTemplates(ctx)
	if err != nil {
		t.Fatalf("list templates: %v", err)
	}
	if len(tmpls) != 10 {
		t.Fatalf("expected 10 seeded templates, got %d", len(tmpls))
	}
	wp, err := s.GetServiceTemplate(ctx, "wordpress")
	if err != nil || wp.Kind != "service" || !wp.Active {
		t.Fatalf("unexpected wordpress template: %+v err=%v", wp, err)
	}

	set, err := s.GetSettings(ctx)
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	if set.DefaultPlanID != "hobby" || set.DefaultCPU != 0.1 || set.DefaultMemoryMB != 128 || len(set.Regions) != 4 {
		t.Fatalf("unexpected settings: %+v", set)
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

func TestMemoryStoreSumUsageByMetric(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()

	recs := []domain.UsageRecord{
		{ID: "1", OrgID: "o1", Metric: "compute_hours", Quantity: 10},
		{ID: "2", OrgID: "o1", Metric: "compute_hours", Quantity: 5},
		{ID: "3", OrgID: "o2", Metric: "egress_gb", Quantity: 3},
	}
	for i := range recs {
		if err := s.AddUsage(ctx, &recs[i]); err != nil {
			t.Fatalf("add usage: %v", err)
		}
	}

	totals, err := s.SumUsageByMetric(ctx)
	if err != nil {
		t.Fatalf("SumUsageByMetric: %v", err)
	}
	if totals["compute_hours"] != 15 || totals["egress_gb"] != 3 {
		t.Fatalf("unexpected totals: %+v", totals)
	}
}

func TestMemoryStoreUpdateDatabase(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()

	d := &domain.Database{ID: "db1", OrgID: "o1", Name: "pg", Engine: "postgresql", Status: "deploying"}
	if err := s.CreateDatabase(ctx, d); err != nil {
		t.Fatalf("create: %v", err)
	}
	d.Status = "running"
	if err := s.UpdateDatabase(ctx, d); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := s.GetDatabase(ctx, "db1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != "running" {
		t.Fatalf("status = %q, want running", got.Status)
	}
	// Updating a missing database is a not-found error.
	if err := s.UpdateDatabase(ctx, &domain.Database{ID: "missing"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update missing: expected ErrNotFound, got %v", err)
	}
}
