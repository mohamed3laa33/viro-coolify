package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pashagolub/pgxmock/v4"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
)

// newMockStore returns a PostgresStore backed by a pgxmock pool. pgxmock's
// PgxPoolIface satisfies the unexported pgxPool interface used by PostgresStore.
func newMockStore(t *testing.T) (*PostgresStore, pgxmock.PgxPoolIface) {
	t.Helper()
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("new mock pool: %v", err)
	}
	return newPostgresStoreWithPool(mock), mock
}

func TestCreateUser_Happy(t *testing.T) {
	s, mock := newMockStore(t)
	defer mock.Close()

	u := &domain.User{ID: "u1", Email: "Alice@Example.com", Name: "Alice", CreatedAt: time.Now()}
	mock.ExpectExec("INSERT INTO users").
		WithArgs("u1", "alice@example.com", "Alice", "", false, u.CreatedAt).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	if err := s.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestCreateUser_Conflict(t *testing.T) {
	s, mock := newMockStore(t)
	defer mock.Close()

	u := &domain.User{ID: "u1", Email: "alice@example.com", CreatedAt: time.Now()}
	mock.ExpectExec("INSERT INTO users").
		WithArgs("u1", "alice@example.com", "", "", false, u.CreatedAt).
		WillReturnError(&pgconn.PgError{Code: "23505"})

	err := s.CreateUser(context.Background(), u)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestCreateRefreshToken_Postgres(t *testing.T) {
	s, mock := newMockStore(t)
	defer mock.Close()

	rt := &domain.RefreshToken{ID: "jti-1", UserID: "u1", CreatedAt: time.Now()}
	mock.ExpectExec("INSERT INTO refresh_tokens").
		WithArgs("jti-1", "u1", false, rt.CreatedAt).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	if err := s.CreateRefreshToken(context.Background(), rt); err != nil {
		t.Fatalf("CreateRefreshToken: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestGetRefreshToken_Postgres(t *testing.T) {
	s, mock := newMockStore(t)
	defer mock.Close()

	created := time.Now()
	mock.ExpectQuery("SELECT id, user_id, revoked, created_at FROM refresh_tokens").
		WithArgs("jti-1").
		WillReturnRows(pgxmock.NewRows([]string{"id", "user_id", "revoked", "created_at"}).
			AddRow("jti-1", "u1", true, created))

	got, err := s.GetRefreshToken(context.Background(), "jti-1")
	if err != nil {
		t.Fatalf("GetRefreshToken: %v", err)
	}
	if got.ID != "jti-1" || got.UserID != "u1" || !got.Revoked {
		t.Fatalf("unexpected record: %+v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestGetRefreshToken_NotFound_Postgres(t *testing.T) {
	s, mock := newMockStore(t)
	defer mock.Close()

	mock.ExpectQuery("SELECT id, user_id, revoked, created_at FROM refresh_tokens").
		WithArgs("missing").
		WillReturnError(pgx.ErrNoRows)

	if _, err := s.GetRefreshToken(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestRevokeRefreshToken_Postgres(t *testing.T) {
	s, mock := newMockStore(t)
	defer mock.Close()

	mock.ExpectExec("UPDATE refresh_tokens SET revoked = true WHERE id").
		WithArgs("jti-1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	if err := s.RevokeRefreshToken(context.Background(), "jti-1"); err != nil {
		t.Fatalf("RevokeRefreshToken: %v", err)
	}

	// No rows affected -> ErrNotFound.
	mock.ExpectExec("UPDATE refresh_tokens SET revoked = true WHERE id").
		WithArgs("missing").
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	if err := s.RevokeRefreshToken(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestRevokeAllUserRefreshTokens_Postgres(t *testing.T) {
	s, mock := newMockStore(t)
	defer mock.Close()

	mock.ExpectExec("UPDATE refresh_tokens SET revoked = true WHERE user_id").
		WithArgs("u1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 2))
	if err := s.RevokeAllUserRefreshTokens(context.Background(), "u1"); err != nil {
		t.Fatalf("RevokeAllUserRefreshTokens: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestGetUserByEmail_NotFound(t *testing.T) {
	s, mock := newMockStore(t)
	defer mock.Close()

	mock.ExpectQuery("SELECT id, email, name, password_hash, is_admin, created_at FROM users WHERE lower\\(email\\)").
		WithArgs("ghost@example.com").
		WillReturnError(pgx.ErrNoRows)

	_, err := s.GetUserByEmail(context.Background(), "Ghost@Example.com")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestUpsertPlan(t *testing.T) {
	s, mock := newMockStore(t)
	defer mock.Close()

	p := &domain.Plan{ID: "hobby", Name: "Hobby", Currency: "usd", MaxCPU: 0.5, MaxMemoryMB: 512, MaxApps: 3, IsDefault: true, SortOrder: 1, Active: true}
	mock.ExpectExec("INSERT INTO plans").
		WithArgs("hobby", "Hobby", "", 0, "usd", 0, 0, "", 0.5, 512, 3, true, 1, true).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	if err := s.UpsertPlan(context.Background(), p); err != nil {
		t.Fatalf("UpsertPlan: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestListPlans(t *testing.T) {
	s, mock := newMockStore(t)
	defer mock.Close()

	rows := pgxmock.NewRows([]string{
		"id", "name", "description", "price_cents", "currency", "included_hours", "overage_per_hour_cents",
		"stripe_price_id", "max_cpu", "max_memory_mb", "max_apps", "is_default", "sort_order", "active",
	}).
		AddRow("hobby", "Hobby", "desc", 0, "usd", 160, 0, "", 0.5, 512, 3, true, 1, true).
		AddRow("launch", "Launch", "desc", 2900, "usd", 720, 2, "", 1.0, 1024, 20, false, 2, true)

	mock.ExpectQuery("SELECT id, name, description, price_cents").WillReturnRows(rows)

	got, err := s.ListPlans(context.Background())
	if err != nil {
		t.Fatalf("ListPlans: %v", err)
	}
	if len(got) != 2 || got[0].ID != "hobby" || got[1].ID != "launch" {
		t.Fatalf("unexpected plans: %+v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestGetSettings(t *testing.T) {
	s, mock := newMockStore(t)
	defer mock.Close()

	rows := pgxmock.NewRows([]string{
		"default_cpu", "default_memory_mb", "default_plan_id", "cpu_overcommit_factor",
		"memory_overcommit_factor", "default_region", "regions", "grace_past_due", "default_spend_cap_cents",
	}).AddRow(0.25, 256, "hobby", 0.2, 0.35, "fra1", []string{"fra1", "nyc1"}, false, int64(0))

	mock.ExpectQuery("SELECT default_cpu, default_memory_mb").WillReturnRows(rows)

	got, err := s.GetSettings(context.Background())
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if got.DefaultPlanID != "hobby" || got.CPUOvercommitFactor != 0.2 || got.MemoryOvercommitFactor != 0.35 {
		t.Fatalf("unexpected settings: %+v", got)
	}
	if len(got.Regions) != 2 {
		t.Fatalf("unexpected regions: %+v", got.Regions)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestCreateAppAndListByOrg(t *testing.T) {
	s, mock := newMockStore(t)
	defer mock.Close()

	now := time.Now()
	a := &domain.App{ID: "a1", OrgID: "o1", ProjectID: "p1", Name: "web", CPU: 0.5, MemoryMB: 256, Status: "running", CreatedAt: now}
	mock.ExpectExec("INSERT INTO apps").
		WithArgs("a1", "o1", "p1", "", "web", "", "", "", 0.5, 256, "running", "", "", "", "", now).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	if err := s.CreateApp(context.Background(), a); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	rows := pgxmock.NewRows([]string{
		"id", "org_id", "project_id", "coolify_uuid", "name", "git_repository", "git_branch",
		"build_pack", "cpu", "memory_mb", "status", "namespace", "release", "host", "image", "created_at",
	}).AddRow("a1", "o1", "p1", "", "web", "", "", "", 0.5, 256, "running", "", "", "", "", now)

	mock.ExpectQuery("SELECT id, org_id, project_id, coolify_uuid, name, git_repository").
		WithArgs("o1").
		WillReturnRows(rows)

	got, err := s.ListAppsByOrg(context.Background(), "o1")
	if err != nil {
		t.Fatalf("ListAppsByOrg: %v", err)
	}
	if len(got) != 1 || got[0].ID != "a1" || got[0].OrgID != "o1" {
		t.Fatalf("unexpected apps: %+v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestBuildCRUD_Postgres(t *testing.T) {
	s, mock := newMockStore(t)
	defer mock.Close()

	now := time.Now()
	var zero time.Time
	b := &domain.Build{
		ID: "b1", AppID: "a1", OrgID: "o1", Status: domain.BuildBuilding,
		CommitRef: "main", CreatedAt: now,
	}
	mock.ExpectExec("INSERT INTO builds").
		WithArgs("b1", "a1", "o1", "building", "main", "", "", now, zero).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	if err := s.CreateBuild(context.Background(), b); err != nil {
		t.Fatalf("CreateBuild: %v", err)
	}

	// List by app (newest first).
	rows := pgxmock.NewRows([]string{
		"id", "app_id", "org_id", "status", "commit_ref", "image", "logs", "created_at", "finished_at",
	}).AddRow("b1", "a1", "o1", "succeeded", "main", "ghcr.io/x:1", "", now, now)
	mock.ExpectQuery("SELECT id, app_id, org_id, status, commit_ref, image, logs, created_at, finished_at\\s+FROM builds WHERE app_id").
		WithArgs("a1").
		WillReturnRows(rows)
	got, err := s.ListBuildsByApp(context.Background(), "a1")
	if err != nil {
		t.Fatalf("ListBuildsByApp: %v", err)
	}
	if len(got) != 1 || got[0].ID != "b1" || got[0].Status != domain.BuildSucceeded {
		t.Fatalf("unexpected builds: %+v", got)
	}

	// Update.
	b.Status = domain.BuildFailed
	b.Logs = "boom"
	b.FinishedAt = now
	mock.ExpectExec("UPDATE builds SET").
		WithArgs("b1", "a1", "o1", "failed", "main", "", "boom", now, now).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	if err := s.UpdateBuild(context.Background(), b); err != nil {
		t.Fatalf("UpdateBuild: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestAddMembership_Conflict(t *testing.T) {
	s, mock := newMockStore(t)
	defer mock.Close()

	m := domain.Membership{OrgID: "o1", UserID: "u1", Role: domain.RoleOwner}
	mock.ExpectExec("INSERT INTO memberships").
		WithArgs("o1", "u1", "owner").
		WillReturnError(&pgconn.PgError{Code: "23505"})

	err := s.AddMembership(context.Background(), m)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestSumUsageByMetric(t *testing.T) {
	s, mock := newMockStore(t)
	defer mock.Close()

	rows := pgxmock.NewRows([]string{"metric", "sum"}).
		AddRow("compute_hours", int64(42)).
		AddRow("egress_gb", int64(7))

	mock.ExpectQuery("SELECT metric, sum\\(quantity\\) FROM usage_records GROUP BY metric").
		WillReturnRows(rows)

	got, err := s.SumUsageByMetric(context.Background())
	if err != nil {
		t.Fatalf("SumUsageByMetric: %v", err)
	}
	if got["compute_hours"] != 42 || got["egress_gb"] != 7 {
		t.Fatalf("unexpected totals: %+v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestPostgresSetAppEnvWithSecret(t *testing.T) {
	s, mock := newMockStore(t)
	defer mock.Close()

	mock.ExpectExec("INSERT INTO app_env").
		WithArgs("app1", "API_KEY", "v1:ciphertext", true).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	if err := s.SetAppEnv(context.Background(), "app1", "API_KEY", "v1:ciphertext", true); err != nil {
		t.Fatalf("SetAppEnv: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestPostgresListAppEnv(t *testing.T) {
	s, mock := newMockStore(t)
	defer mock.Close()

	rows := pgxmock.NewRows([]string{"key", "value", "secret"}).
		AddRow("PLAIN", "v", false).
		AddRow("SECRET", "v1:enc", true)
	mock.ExpectQuery("SELECT key, value, secret FROM app_env WHERE app_id = \\$1").
		WithArgs("app1").
		WillReturnRows(rows)

	got, err := s.ListAppEnv(context.Background(), "app1")
	if err != nil {
		t.Fatalf("ListAppEnv: %v", err)
	}
	if len(got) != 2 || got[1].Key != "SECRET" || !got[1].Secret {
		t.Fatalf("unexpected entries: %+v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestPostgresCreateAndListAuditEvents(t *testing.T) {
	s, mock := newMockStore(t)
	defer mock.Close()

	at := time.Now()
	mock.ExpectExec("INSERT INTO audit_events").
		WithArgs("e1", "org1", "u1", "a@b.com", "secret.set", "app_env", "app1/API_KEY", "", at).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	if err := s.CreateAuditEvent(context.Background(), &domain.AuditEvent{
		ID: "e1", OrgID: "org1", ActorUserID: "u1", ActorEmail: "a@b.com",
		Action: "secret.set", TargetType: "app_env", TargetID: "app1/API_KEY", At: at,
	}); err != nil {
		t.Fatalf("CreateAuditEvent: %v", err)
	}

	rows := pgxmock.NewRows([]string{"id", "org_id", "actor_user_id", "actor_email", "action", "target_type", "target_id", "metadata", "at"}).
		AddRow("e1", "org1", "u1", "a@b.com", "secret.set", "app_env", "app1/API_KEY", "", at)
	mock.ExpectQuery("SELECT id, org_id, actor_user_id, actor_email, action, target_type, target_id, metadata, at\\s+FROM audit_events WHERE org_id = \\$1").
		WithArgs("org1", 100).
		WillReturnRows(rows)
	got, err := s.ListAuditEvents(context.Background(), domain.AuditFilter{OrgID: "org1"})
	if err != nil {
		t.Fatalf("ListAuditEvents: %v", err)
	}
	if len(got) != 1 || got[0].Action != "secret.set" {
		t.Fatalf("unexpected events: %+v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}
