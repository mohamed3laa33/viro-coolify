package store

import (
	"context"
	"errors"
	"testing"
	"time"

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

	d := &domain.Database{
		ID: "db1", OrgID: "o1", Name: "pg", Engine: "postgresql", Status: "deploying",
		StorageGB: 5, Username: "app_user", Password: "s3cret", DatabaseName: "app",
	}
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
	// Storage + credentials round-trip.
	if got.StorageGB != 5 || got.Username != "app_user" || got.Password != "s3cret" || got.DatabaseName != "app" {
		t.Fatalf("storage/creds not round-tripped: %+v", got)
	}
	// Updating a missing database is a not-found error.
	if err := s.UpdateDatabase(ctx, &domain.Database{ID: "missing"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update missing: expected ErrNotFound, got %v", err)
	}
}

func TestMemoryStoreBuilds(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()

	now := time.Now()
	b1 := &domain.Build{ID: "b1", AppID: "a1", OrgID: "o1", Status: domain.BuildBuilding, CreatedAt: now}
	b2 := &domain.Build{ID: "b2", AppID: "a1", OrgID: "o1", Status: domain.BuildPending, CreatedAt: now.Add(time.Second)}
	if err := s.CreateBuild(ctx, b1); err != nil {
		t.Fatalf("create b1: %v", err)
	}
	if err := s.CreateBuild(ctx, b2); err != nil {
		t.Fatalf("create b2: %v", err)
	}
	// Duplicate id conflicts.
	if err := s.CreateBuild(ctx, b1); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate create: expected ErrConflict, got %v", err)
	}

	// List newest-first.
	list, err := s.ListBuildsByApp(ctx, "a1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 || list[0].ID != "b2" {
		t.Fatalf("expected newest-first [b2,b1], got %+v", list)
	}

	// Update.
	b1.Status = domain.BuildSucceeded
	b1.Image = "img:1"
	if err := s.UpdateBuild(ctx, b1); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := s.GetBuild(ctx, "b1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != domain.BuildSucceeded || got.Image != "img:1" {
		t.Fatalf("unexpected build: %+v", got)
	}

	// Missing get/update.
	if _, err := s.GetBuild(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get missing: expected ErrNotFound, got %v", err)
	}
	if err := s.UpdateBuild(ctx, &domain.Build{ID: "nope"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update missing: expected ErrNotFound, got %v", err)
	}
}

func TestMemoryStoreAppEnvSecretFlag(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()

	if err := s.SetAppEnv(ctx, "app1", "PLAIN", "v", false); err != nil {
		t.Fatalf("set plain: %v", err)
	}
	if err := s.SetAppEnv(ctx, "app1", "SECRET", "enc", true); err != nil {
		t.Fatalf("set secret: %v", err)
	}
	entries, err := s.ListAppEnv(ctx, "app1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(entries))
	}
	// Sorted by key: PLAIN, SECRET.
	if entries[0].Key != "PLAIN" || entries[0].Secret {
		t.Fatalf("plain entry wrong: %+v", entries[0])
	}
	if entries[1].Key != "SECRET" || !entries[1].Secret || entries[1].Value != "enc" {
		t.Fatalf("secret entry wrong: %+v", entries[1])
	}
	// GetAppEnv returns the at-rest values keyed by name.
	raw, _ := s.GetAppEnv(ctx, "app1")
	if raw["SECRET"] != "enc" || raw["PLAIN"] != "v" {
		t.Fatalf("get app env: %+v", raw)
	}
}

func TestMemoryStoreAuditLog(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()

	for i, a := range []string{"plan.create", "secret.set", "auth.login"} {
		e := &domain.AuditEvent{
			ID: string(rune('a' + i)), OrgID: "org1", Action: a,
			At: time.Now().Add(time.Duration(i) * time.Second),
		}
		if err := s.CreateAuditEvent(ctx, e); err != nil {
			t.Fatalf("create audit: %v", err)
		}
	}
	// Platform-level event (no org).
	_ = s.CreateAuditEvent(ctx, &domain.AuditEvent{ID: "p1", OrgID: "", Action: "settings.update", At: time.Now()})

	orgEvents, err := s.ListAuditEvents(ctx, domain.AuditFilter{OrgID: "org1", Limit: 10})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if len(orgEvents) != 3 {
		t.Fatalf("want 3 org events, got %d", len(orgEvents))
	}
	// Most-recent-first.
	if orgEvents[0].Action != "auth.login" {
		t.Fatalf("expected newest first, got %q", orgEvents[0].Action)
	}
	platEvents, _ := s.ListAuditEvents(ctx, domain.AuditFilter{OrgID: "", Limit: 10})
	if len(platEvents) != 1 || platEvents[0].Action != "settings.update" {
		t.Fatalf("platform events wrong: %+v", platEvents)
	}
}

func TestMemoryStoreApiTokens(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()

	if err := s.CreateUser(ctx, &domain.User{ID: "u1", Email: "a@example.com", Name: "A"}); err != nil {
		t.Fatalf("create user: %v", err)
	}

	tok := &domain.ApiToken{
		ID: "t1", UserID: "u1", Name: "ci",
		TokenHash: "hash1", Prefix: "vrt_ab12",
		Scopes:    []string{"read"},
		CreatedAt: time.Now(),
	}
	if err := s.CreateApiToken(ctx, tok); err != nil {
		t.Fatalf("create token: %v", err)
	}
	// Duplicate hash conflicts.
	if err := s.CreateApiToken(ctx, &domain.ApiToken{ID: "t2", UserID: "u1", TokenHash: "hash1"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("dup hash = %v, want ErrConflict", err)
	}

	got, err := s.GetApiTokenByHash(ctx, "hash1")
	if err != nil {
		t.Fatalf("get by hash: %v", err)
	}
	if got.ID != "t1" || got.UserID != "u1" {
		t.Fatalf("unexpected token: %+v", got)
	}
	// Mutating the returned slice must not affect stored state.
	got.Scopes[0] = "mutated"
	again, _ := s.GetApiTokenByHash(ctx, "hash1")
	if again.Scopes[0] != "read" {
		t.Fatalf("stored scopes were mutated via returned copy: %v", again.Scopes)
	}

	// Touch updates last-used.
	now := time.Now()
	if err := s.TouchApiToken(ctx, "t1", now); err != nil {
		t.Fatalf("touch: %v", err)
	}
	touched, _ := s.GetApiTokenByHash(ctx, "hash1")
	if touched.LastUsedAt.IsZero() {
		t.Fatalf("last-used not updated")
	}

	list, err := s.ListApiTokensByUser(ctx, "u1")
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %v, err = %v", list, err)
	}

	// Cross-user delete is a no-op (ErrNotFound).
	if err := s.DeleteApiToken(ctx, "other", "t1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-user delete = %v, want ErrNotFound", err)
	}
	if err := s.DeleteApiToken(ctx, "u1", "t1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetApiTokenByHash(ctx, "hash1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after delete = %v, want ErrNotFound", err)
	}
}
