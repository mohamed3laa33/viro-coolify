package store

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pashagolub/pgxmock/v4"
)

// TestMigrate_AdvisoryLockOrder asserts that Migrate takes the session advisory
// lock first, ensures schema_migrations, processes every (already-applied)
// migration, and releases the lock last — in order. With every embedded
// migration reported as already-applied, no body/INSERT runs, so the only
// statements are the lock, the table-ensure, the per-migration existence checks
// and the unlock.
func TestMigrate_AdvisoryLockOrder(t *testing.T) {
	s, mock := newMockStore(t)
	defer mock.Close()

	// 1. Session advisory lock for the whole run.
	mock.ExpectExec(`SELECT pg_advisory_lock\(\$1\)`).
		WithArgs(migrationsAdvisoryLockKey).
		WillReturnResult(pgxmock.NewResult("SELECT", 1))
	// 2. Ensure the tracking table.
	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS schema_migrations`).
		WillReturnResult(pgxmock.NewResult("CREATE", 0))

	// 3. Each embedded migration is reported already-applied, so it is skipped
	//    (no Begin/Exec/Commit). The existence checks run in sorted filename order.
	names := embeddedMigrationNames(t)
	for _, name := range names {
		mock.ExpectQuery(`SELECT EXISTS \(SELECT 1 FROM schema_migrations WHERE version = \$1\)`).
			WithArgs(name).
			WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
	}

	// 4. Release the lock last.
	mock.ExpectExec(`SELECT pg_advisory_unlock\(\$1\)`).
		WithArgs(migrationsAdvisoryLockKey).
		WillReturnResult(pgxmock.NewResult("SELECT", 1))

	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestApplyMigration_BodyFailureRollsBack asserts that when a migration body
// fails, the transaction is rolled back and the version is NEVER recorded
// (no INSERT into schema_migrations, no Commit).
func TestApplyMigration_BodyFailureRollsBack(t *testing.T) {
	s, mock := newMockStore(t)
	defer mock.Close()

	mock.ExpectBegin()
	mock.ExpectExec(`bad sql`).
		WillReturnError(&pgconn.PgError{Code: "42601"}) // syntax_error
	mock.ExpectRollback()

	sess, release, err := s.acquireMigrateSession(context.Background())
	if err != nil {
		t.Fatalf("acquire session: %v", err)
	}
	defer release()

	err = applyMigration(context.Background(), sess, "0099_bad.sql", "bad sql")
	if err == nil {
		t.Fatal("expected applyMigration to fail on a bad body")
	}
	// The INSERT into schema_migrations and a Commit must NOT have happened; the
	// only expectations were Begin/Exec(body)/Rollback. If applyMigration had
	// recorded the version, an unmatched ExpectExec(INSERT) would be required and
	// ExpectationsWereMet would still pass — so we assert the inverse by ensuring
	// no extra calls were made beyond what we set up.
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestApplyMigration_Commits asserts the happy path: Begin → Exec body → Exec
// INSERT version → Commit, all in one transaction.
func TestApplyMigration_Commits(t *testing.T) {
	s, mock := newMockStore(t)
	defer mock.Close()

	mock.ExpectBegin()
	mock.ExpectExec(`CREATE TABLE widgets`).
		WillReturnResult(pgxmock.NewResult("CREATE", 0))
	mock.ExpectExec(`INSERT INTO schema_migrations`).
		WithArgs("0099_widgets.sql").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	sess, release, err := s.acquireMigrateSession(context.Background())
	if err != nil {
		t.Fatalf("acquire session: %v", err)
	}
	defer release()

	if err := applyMigration(context.Background(), sess, "0099_widgets.sql", "CREATE TABLE widgets"); err != nil {
		t.Fatalf("applyMigration: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestMapErr_ClassifiesPgErrors asserts mapErr translates the referential/shape
// violation codes into the right sentinels so the HTTP layer returns 4xx, not 500.
func TestMapErr_ClassifiesPgErrors(t *testing.T) {
	cases := []struct {
		code string
		want error
	}{
		{"23505", ErrConflict}, // unique_violation
		{"23503", ErrInvalid},  // foreign_key_violation
		{"23502", ErrInvalid},  // not_null_violation
		{"23514", ErrInvalid},  // check_violation
	}
	for _, c := range cases {
		got := mapErr(&pgconn.PgError{Code: c.code})
		if !errors.Is(got, c.want) {
			t.Fatalf("mapErr(%s) = %v, want %v", c.code, got, c.want)
		}
	}
	// A code we do not classify must pass through unchanged (a genuine 500).
	raw := &pgconn.PgError{Code: "42601"} // syntax_error
	if got := mapErr(raw); errors.Is(got, ErrInvalid) || errors.Is(got, ErrConflict) || errors.Is(got, ErrNotFound) {
		t.Fatalf("mapErr(42601) should pass through, got %v", got)
	}
}

// embeddedMigrationNames returns the sorted .sql migration filenames so the lock
// test can expect exactly one existence check per embedded migration.
func embeddedMigrationNames(t *testing.T) []string {
	t.Helper()
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}
