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
		"memory_overcommit_factor", "default_region", "regions",
	}).AddRow(0.25, 256, "hobby", 0.2, 0.35, "fra1", []string{"fra1", "nyc1"})

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
