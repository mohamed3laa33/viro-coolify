package store

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
)

// Connection-pool lifetime bounds. Recycling connections caps the blast radius
// of a half-dead connection and lets the pool shrink back toward MinConns when
// idle.
const (
	dbMaxConnLifetime = time.Hour
	dbMaxConnIdleTime = 30 * time.Minute
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// pgxPool is the subset of pgxpool.Pool used by PostgresStore. It is satisfied
// by *pgxpool.Pool in production and by pgxmock connections in tests, which lets
// us inject a query-level mock without a live database.
type pgxPool interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Begin(ctx context.Context) (pgx.Tx, error)
}

// PostgresStore is a Postgres-backed implementation of Store using pgx v5.
type PostgresStore struct {
	pool pgxPool
}

// NewPostgresStore opens a tuned pgx connection pool against dsn and verifies
// it. maxConns/minConns bound the pool size; non-positive values fall back to
// sane defaults so callers can pass zero to accept the defaults.
func NewPostgresStore(ctx context.Context, dsn string, maxConns, minConns int) (*PostgresStore, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("store: parse dsn: %w", err)
	}
	if maxConns <= 0 {
		maxConns = 10
	}
	if minConns < 0 {
		minConns = 0
	}
	if minConns > maxConns {
		minConns = maxConns
	}
	cfg.MaxConns = int32(maxConns)
	cfg.MinConns = int32(minConns)
	cfg.MaxConnLifetime = dbMaxConnLifetime
	cfg.MaxConnIdleTime = dbMaxConnIdleTime

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("store: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	return &PostgresStore{pool: pool}, nil
}

// newPostgresStoreWithPool builds a store around an injected pool (used in tests).
func newPostgresStoreWithPool(pool pgxPool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

// Ping verifies the database connection is alive (used by readiness probes).
func (s *PostgresStore) Ping(ctx context.Context) error {
	if p, ok := s.pool.(*pgxpool.Pool); ok {
		return p.Ping(ctx)
	}
	// Injected (mock) pools have no Ping; treat them as healthy.
	return nil
}

// Close releases the underlying pool when it owns one.
func (s *PostgresStore) Close() {
	if p, ok := s.pool.(*pgxpool.Pool); ok {
		p.Close()
	}
}

var _ Store = (*PostgresStore)(nil)

// WithTx runs fn inside a single Postgres transaction. It begins a tx, builds a
// transaction-scoped *PostgresStore backed by the pgx.Tx (which satisfies the
// pgxPool abstraction), runs fn against it, and commits on success or rolls back
// on any error or panic. This gives Signup/CreateOrganization/AcceptInvitation
// true all-or-nothing multi-write semantics.
func (s *PostgresStore) WithTx(ctx context.Context, fn func(tx Store) error) (err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return mapErr(err)
	}
	committed := false
	defer func() {
		if !committed {
			// Roll back on error or panic. Ignore the rollback error when the tx is
			// already closed (e.g. after a successful commit path) — Rollback on a
			// committed tx returns pgx.ErrTxClosed which we don't want to surface.
			_ = tx.Rollback(ctx)
		}
	}()
	txStore := newPostgresStoreWithPool(tx)
	if err := fn(txStore); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return mapErr(err)
	}
	committed = true
	return nil
}

// mapErr converts pgx/pg errors into the store's sentinel errors so the HTTP
// layer can map them to the right status code instead of leaking a raw 500:
//   - no rows                -> ErrNotFound (404)
//   - unique_violation 23505 -> ErrConflict (409)
//   - foreign_key 23503, not_null 23502, check 23514 -> ErrInvalid (400/422)
//
// Everything else is returned unchanged (a genuine 500).
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505": // unique_violation
			return ErrConflict
		case "23503", // foreign_key_violation
			"23502", // not_null_violation
			"23514": // check_violation
			return ErrInvalid
		}
	}
	return err
}

// appendPage appends LIMIT/OFFSET placeholders to sql (and the matching values to
// args) when p is bounded (Limit > 0). An unbounded page leaves the query
// untouched so internal callers can read the full set. OFFSET is emitted only
// when positive to keep the common first-page query simple.
func appendPage(sql string, args []any, p Page) (string, []any) {
	if !p.Bounded() {
		return sql, args
	}
	args = append(args, p.Limit)
	sql += fmt.Sprintf(" LIMIT $%d", len(args))
	if p.Offset > 0 {
		args = append(args, p.Offset)
		sql += fmt.Sprintf(" OFFSET $%d", len(args))
	}
	return sql, args
}

// ---- Migrations ----

// migrationsAdvisoryLockKey is the constant key for the session-level advisory
// lock Migrate holds for its whole run, so two replicas booting concurrently
// serialize their migration runs (the loser blocks until the winner finishes and
// then finds every version already applied). The value is arbitrary but must be
// stable across replicas; it is namespaced to "vortex migrations".
const migrationsAdvisoryLockKey int64 = 0x564f52_4d494752 // "VOR_MIGR"

// migrateSession is the connection-scoped subset of operations Migrate needs.
// Both *pgxpool.Conn (a single pooled connection) and the injected mock pool
// satisfy it, so the session-level advisory lock and every per-migration
// transaction run on ONE connection — required for pg_advisory_lock/unlock to
// refer to the same session.
type migrateSession interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Begin(ctx context.Context) (pgx.Tx, error)
}

// acquireMigrateSession returns a single connection to run migrations on, plus a
// release func. For a real pool it checks out one *pgxpool.Conn so the advisory
// lock is session-stable; for an injected (mock) pool it returns the pool itself
// (a single mock session) with a no-op release.
func (s *PostgresStore) acquireMigrateSession(ctx context.Context) (migrateSession, func(), error) {
	if p, ok := s.pool.(*pgxpool.Pool); ok {
		conn, err := p.Acquire(ctx)
		if err != nil {
			return nil, nil, err
		}
		return conn, conn.Release, nil
	}
	return s.pool, func() {}, nil
}

// Migrate applies all embedded SQL migrations, tracking applied versions in the
// schema_migrations table. It is idempotent and concurrency-safe:
//
//   - The whole run holds a SESSION-level advisory lock (pg_advisory_lock), so two
//     replicas booting at once serialize instead of racing the same migration.
//   - Each pending migration applies its BODY and records its version in a SINGLE
//     transaction (Begin → Exec body → Exec insert → Commit, Rollback on any
//     error). A failure therefore can never leave a half-applied schema recorded
//     as un-applied — either both the body and the version land, or neither does.
//
// Migrations are applied in filename order; already-applied versions are skipped.
func (s *PostgresStore) Migrate(ctx context.Context) (err error) {
	sess, release, err := s.acquireMigrateSession(ctx)
	if err != nil {
		return fmt.Errorf("store: acquire migration session: %w", err)
	}
	defer release()

	// Serialize concurrent replica boots for the whole run. The lock MUST be
	// released explicitly: a session-level pg_advisory_lock persists for the life
	// of the connection, and acquireMigrateSession returns a *pgxpool.Conn that
	// Release() puts back into the pool ALIVE — so a leaked lock would block every
	// later boot until the connection is reaped (up to MaxConnLifetime). The unlock
	// therefore runs on a non-cancellable context so it always fires even when ctx
	// was cancelled by a deploy timeout / SIGTERM during migration.
	if _, err := sess.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationsAdvisoryLockKey); err != nil {
		return fmt.Errorf("store: acquire migration lock: %w", err)
	}
	defer func() {
		unlockCtx := context.WithoutCancel(ctx)
		if _, uerr := sess.Exec(unlockCtx, `SELECT pg_advisory_unlock($1)`, migrationsAdvisoryLockKey); uerr != nil && err == nil {
			err = fmt.Errorf("store: release migration lock: %w", uerr)
		}
	}()

	if _, err := sess.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version text PRIMARY KEY,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		return fmt.Errorf("store: ensure schema_migrations: %w", err)
	}

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("store: read migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		var exists bool
		if err := sess.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`, name,
		).Scan(&exists); err != nil {
			return fmt.Errorf("store: check migration %s: %w", name, err)
		}
		if exists {
			continue
		}
		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("store: read migration %s: %w", name, err)
		}
		if err := applyMigration(ctx, sess, name, string(body)); err != nil {
			return err
		}
	}
	return nil
}

// applyMigration runs one migration's body and records its version inside a
// single transaction, rolling back on any error so a partial apply is never
// recorded as un-applied.
func applyMigration(ctx context.Context, sess migrateSession, name, body string) error {
	tx, err := sess.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: begin migration %s: %w", name, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()
	if _, err := tx.Exec(ctx, body); err != nil {
		return fmt.Errorf("store: apply migration %s: %w", name, err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO schema_migrations (version) VALUES ($1)`, name,
	); err != nil {
		return fmt.Errorf("store: record migration %s: %w", name, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: commit migration %s: %w", name, err)
	}
	committed = true
	return nil
}

// Seed populates default business config (plans, service templates and platform
// settings) when the corresponding tables are empty. It is idempotent.
func (s *PostgresStore) Seed(ctx context.Context) error {
	var planCount int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM plans`).Scan(&planCount); err != nil {
		return mapErr(err)
	}
	if planCount == 0 {
		for _, p := range defaultPlans() {
			p := p
			if err := s.UpsertPlan(ctx, &p); err != nil {
				return err
			}
		}
	}

	var tmplCount int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM service_templates`).Scan(&tmplCount); err != nil {
		return mapErr(err)
	}
	if tmplCount == 0 {
		for _, t := range defaultTemplates() {
			t := t
			if err := s.UpsertServiceTemplate(ctx, &t); err != nil {
				return err
			}
		}
	}

	var pricingCount int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM pricing_components`).Scan(&pricingCount); err != nil {
		return mapErr(err)
	}
	if pricingCount == 0 {
		for _, p := range defaultPricing() {
			p := p
			if err := s.UpsertPricingComponent(ctx, &p); err != nil {
				return err
			}
		}
	}

	var settingsCount int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM platform_settings`).Scan(&settingsCount); err != nil {
		return mapErr(err)
	}
	if settingsCount == 0 {
		ds := defaultSettings()
		if err := s.UpdateSettings(ctx, &ds); err != nil {
			return err
		}
	}
	return nil
}

// ---- Users ----

func (s *PostgresStore) CreateUser(ctx context.Context, u *domain.User) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO users (id, email, name, password_hash, is_admin, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		u.ID, strings.ToLower(u.Email), u.Name, u.PasswordHash, u.IsAdmin, u.CreatedAt,
	)
	return mapErr(err)
}

func (s *PostgresStore) GetUserByID(ctx context.Context, id string) (*domain.User, error) {
	return s.scanUser(s.pool.QueryRow(ctx,
		`SELECT id, email, name, password_hash, is_admin, created_at FROM users WHERE id = $1`, id))
}

func (s *PostgresStore) GetUserByEmail(ctx context.Context, email string) (*domain.User, error) {
	return s.scanUser(s.pool.QueryRow(ctx,
		`SELECT id, email, name, password_hash, is_admin, created_at FROM users WHERE lower(email) = $1`,
		strings.ToLower(email)))
}

func (s *PostgresStore) scanUser(row pgx.Row) (*domain.User, error) {
	var u domain.User
	if err := row.Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.IsAdmin, &u.CreatedAt); err != nil {
		return nil, mapErr(err)
	}
	return &u, nil
}

// UpdateUser updates the mutable fields of a user. Email is the stable lookup
// key and is not changed here (mirrors the memory store).
func (s *PostgresStore) UpdateUser(ctx context.Context, u *domain.User) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE users SET name = $2, password_hash = $3, is_admin = $4, created_at = $5 WHERE id = $1`,
		u.ID, u.Name, u.PasswordHash, u.IsAdmin, u.CreatedAt,
	)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	// Reflect the persisted email back onto the caller's struct.
	var email string
	if err := s.pool.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, u.ID).Scan(&email); err != nil {
		return mapErr(err)
	}
	u.Email = email
	return nil
}

// ---- Organizations ----

func (s *PostgresStore) CreateOrganization(ctx context.Context, o *domain.Organization) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO organizations (id, name, slug, billing_email, spend_cap_cents, created_at) VALUES ($1, $2, $3, $4, $5, $6)`,
		o.ID, o.Name, o.Slug, o.BillingEmail, o.SpendCapCents, o.CreatedAt,
	)
	return mapErr(err)
}

func (s *PostgresStore) GetOrganization(ctx context.Context, id string) (*domain.Organization, error) {
	var o domain.Organization
	err := s.pool.QueryRow(ctx,
		`SELECT id, name, slug, billing_email, spend_cap_cents, created_at FROM organizations WHERE id = $1`, id,
	).Scan(&o.ID, &o.Name, &o.Slug, &o.BillingEmail, &o.SpendCapCents, &o.CreatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &o, nil
}

func (s *PostgresStore) ListOrganizationsForUser(ctx context.Context, userID string) ([]domain.Organization, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT o.id, o.name, o.slug, o.billing_email, o.spend_cap_cents, o.created_at
		 FROM organizations o
		 JOIN memberships m ON m.org_id = o.id
		 WHERE m.user_id = $1`, userID,
	)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var out []domain.Organization
	for rows.Next() {
		var o domain.Organization
		if err := rows.Scan(&o.ID, &o.Name, &o.Slug, &o.BillingEmail, &o.SpendCapCents, &o.CreatedAt); err != nil {
			return nil, mapErr(err)
		}
		out = append(out, o)
	}
	return out, mapErr(rows.Err())
}

// UpdateOrg persists the mutable org fields (name, billing email, spend cap). The
// org ID is the stable lookup key and is not changed here.
func (s *PostgresStore) UpdateOrg(ctx context.Context, o *domain.Organization) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE organizations SET name = $2, billing_email = $3, spend_cap_cents = $4 WHERE id = $1`,
		o.ID, o.Name, o.BillingEmail, o.SpendCapCents,
	)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- Memberships ----

func (s *PostgresStore) AddMembership(ctx context.Context, m domain.Membership) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO memberships (org_id, user_id, role) VALUES ($1, $2, $3)`,
		m.OrgID, m.UserID, string(m.Role),
	)
	return mapErr(err)
}

func (s *PostgresStore) GetMembership(ctx context.Context, orgID, userID string) (*domain.Membership, error) {
	var m domain.Membership
	var role string
	err := s.pool.QueryRow(ctx,
		`SELECT org_id, user_id, role FROM memberships WHERE org_id = $1 AND user_id = $2`, orgID, userID,
	).Scan(&m.OrgID, &m.UserID, &role)
	if err != nil {
		return nil, mapErr(err)
	}
	m.Role = domain.Role(role)
	return &m, nil
}

func (s *PostgresStore) ListMemberships(ctx context.Context, orgID string) ([]domain.Membership, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT org_id, user_id, role FROM memberships WHERE org_id = $1`, orgID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var out []domain.Membership
	for rows.Next() {
		var m domain.Membership
		var role string
		if err := rows.Scan(&m.OrgID, &m.UserID, &role); err != nil {
			return nil, mapErr(err)
		}
		m.Role = domain.Role(role)
		out = append(out, m)
	}
	return out, mapErr(rows.Err())
}

func (s *PostgresStore) UpdateMembershipRole(ctx context.Context, orgID, userID string, role domain.Role) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE memberships SET role = $3 WHERE org_id = $1 AND user_id = $2`,
		orgID, userID, string(role),
	)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) RemoveMembership(ctx context.Context, orgID, userID string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM memberships WHERE org_id = $1 AND user_id = $2`, orgID, userID)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- Apps ----

func (s *PostgresStore) CreateApp(ctx context.Context, a *domain.App) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO apps (id, org_id, project_id, name, git_repository, git_branch, build_pack, cpu, memory_mb, min_replicas, max_replicas, status, namespace, "release", host, image, region, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)`,
		a.ID, a.OrgID, a.ProjectID, a.Name, a.GitRepository, a.GitBranch, a.BuildPack, a.CPU, a.MemoryMB, a.MinReplicas, a.MaxReplicas, a.Status, a.Namespace, a.Release, a.Host, a.Image, a.Region, a.CreatedAt,
	)
	return mapErr(err)
}

func (s *PostgresStore) GetApp(ctx context.Context, id string) (*domain.App, error) {
	return s.scanApp(s.pool.QueryRow(ctx,
		`SELECT id, org_id, project_id, name, git_repository, git_branch, build_pack, cpu, memory_mb, min_replicas, max_replicas, status, namespace, "release", host, image, region, created_at
		 FROM apps WHERE id = $1`, id))
}

func (s *PostgresStore) scanApp(row pgx.Row) (*domain.App, error) {
	var a domain.App
	if err := row.Scan(&a.ID, &a.OrgID, &a.ProjectID, &a.Name, &a.GitRepository, &a.GitBranch, &a.BuildPack, &a.CPU, &a.MemoryMB, &a.MinReplicas, &a.MaxReplicas, &a.Status, &a.Namespace, &a.Release, &a.Host, &a.Image, &a.Region, &a.CreatedAt); err != nil {
		return nil, mapErr(err)
	}
	return &a, nil
}

func (s *PostgresStore) ListAppsByOrg(ctx context.Context, orgID string) ([]domain.App, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, org_id, project_id, name, git_repository, git_branch, build_pack, cpu, memory_mb, min_replicas, max_replicas, status, namespace, "release", host, image, region, created_at
		 FROM apps WHERE org_id = $1`, orgID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make([]domain.App, 0)
	for rows.Next() {
		var a domain.App
		if err := rows.Scan(&a.ID, &a.OrgID, &a.ProjectID, &a.Name, &a.GitRepository, &a.GitBranch, &a.BuildPack, &a.CPU, &a.MemoryMB, &a.MinReplicas, &a.MaxReplicas, &a.Status, &a.Namespace, &a.Release, &a.Host, &a.Image, &a.Region, &a.CreatedAt); err != nil {
			return nil, mapErr(err)
		}
		out = append(out, a)
	}
	return out, mapErr(rows.Err())
}

func (s *PostgresStore) UpdateApp(ctx context.Context, a *domain.App) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE apps SET org_id = $2, project_id = $3, name = $4, git_repository = $5,
		 git_branch = $6, build_pack = $7, cpu = $8, memory_mb = $9, min_replicas = $10, max_replicas = $11,
		 status = $12, namespace = $13, "release" = $14, host = $15, image = $16, region = $17, created_at = $18
		 WHERE id = $1`,
		a.ID, a.OrgID, a.ProjectID, a.Name, a.GitRepository, a.GitBranch, a.BuildPack, a.CPU, a.MemoryMB, a.MinReplicas, a.MaxReplicas, a.Status, a.Namespace, a.Release, a.Host, a.Image, a.Region, a.CreatedAt,
	)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) DeleteApp(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM apps WHERE id = $1`, id)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- Builds ----

func (s *PostgresStore) CreateBuild(ctx context.Context, b *domain.Build) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO builds (id, app_id, org_id, status, commit_ref, image, logs, created_at, finished_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		b.ID, b.AppID, b.OrgID, string(b.Status), b.CommitRef, b.Image, b.Logs, b.CreatedAt, b.FinishedAt,
	)
	return mapErr(err)
}

func (s *PostgresStore) GetBuild(ctx context.Context, id string) (*domain.Build, error) {
	return s.scanBuild(s.pool.QueryRow(ctx,
		`SELECT id, app_id, org_id, status, commit_ref, image, logs, created_at, finished_at
		 FROM builds WHERE id = $1`, id))
}

func (s *PostgresStore) scanBuild(row pgx.Row) (*domain.Build, error) {
	var b domain.Build
	var status string
	if err := row.Scan(&b.ID, &b.AppID, &b.OrgID, &status, &b.CommitRef, &b.Image, &b.Logs, &b.CreatedAt, &b.FinishedAt); err != nil {
		return nil, mapErr(err)
	}
	b.Status = domain.BuildStatus(status)
	return &b, nil
}

func (s *PostgresStore) ListBuildsByApp(ctx context.Context, appID string, p Page) ([]domain.Build, error) {
	sql := `SELECT id, app_id, org_id, status, commit_ref, image, logs, created_at, finished_at
		 FROM builds WHERE app_id = $1 ORDER BY created_at DESC, id DESC`
	args := []any{appID}
	sql, args = appendPage(sql, args, p)
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make([]domain.Build, 0)
	for rows.Next() {
		var b domain.Build
		var status string
		if err := rows.Scan(&b.ID, &b.AppID, &b.OrgID, &status, &b.CommitRef, &b.Image, &b.Logs, &b.CreatedAt, &b.FinishedAt); err != nil {
			return nil, mapErr(err)
		}
		b.Status = domain.BuildStatus(status)
		out = append(out, b)
	}
	return out, mapErr(rows.Err())
}

func (s *PostgresStore) UpdateBuild(ctx context.Context, b *domain.Build) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE builds SET app_id = $2, org_id = $3, status = $4, commit_ref = $5,
		 image = $6, logs = $7, created_at = $8, finished_at = $9
		 WHERE id = $1`,
		b.ID, b.AppID, b.OrgID, string(b.Status), b.CommitRef, b.Image, b.Logs, b.CreatedAt, b.FinishedAt,
	)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- Releases ----

func (s *PostgresStore) CreateRelease(ctx context.Context, rel *domain.Release) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO releases (id, app_id, org_id, revision, image, git_ref, config_hash, cpu, memory_mb, status, note, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		rel.ID, rel.AppID, rel.OrgID, rel.Revision, rel.Image, rel.GitRef, rel.ConfigHash,
		rel.CPU, rel.MemoryMB, string(rel.Status), rel.Note, rel.CreatedAt,
	)
	return mapErr(err)
}

func (s *PostgresStore) GetRelease(ctx context.Context, id string) (*domain.Release, error) {
	return s.scanRelease(s.pool.QueryRow(ctx,
		`SELECT id, app_id, org_id, revision, image, git_ref, config_hash, cpu, memory_mb, status, note, created_at
		 FROM releases WHERE id = $1`, id))
}

func (s *PostgresStore) scanRelease(row pgx.Row) (*domain.Release, error) {
	var rel domain.Release
	var status string
	if err := row.Scan(&rel.ID, &rel.AppID, &rel.OrgID, &rel.Revision, &rel.Image, &rel.GitRef,
		&rel.ConfigHash, &rel.CPU, &rel.MemoryMB, &status, &rel.Note, &rel.CreatedAt); err != nil {
		return nil, mapErr(err)
	}
	rel.Status = domain.ReleaseStatus(status)
	return &rel, nil
}

func (s *PostgresStore) ListReleasesByApp(ctx context.Context, appID string, p Page) ([]domain.Release, error) {
	sql := `SELECT id, app_id, org_id, revision, image, git_ref, config_hash, cpu, memory_mb, status, note, created_at
		 FROM releases WHERE app_id = $1 ORDER BY revision DESC`
	args := []any{appID}
	sql, args = appendPage(sql, args, p)
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make([]domain.Release, 0)
	for rows.Next() {
		var rel domain.Release
		var status string
		if err := rows.Scan(&rel.ID, &rel.AppID, &rel.OrgID, &rel.Revision, &rel.Image, &rel.GitRef,
			&rel.ConfigHash, &rel.CPU, &rel.MemoryMB, &status, &rel.Note, &rel.CreatedAt); err != nil {
			return nil, mapErr(err)
		}
		rel.Status = domain.ReleaseStatus(status)
		out = append(out, rel)
	}
	return out, mapErr(rows.Err())
}

func (s *PostgresStore) UpdateRelease(ctx context.Context, rel *domain.Release) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE releases SET app_id = $2, org_id = $3, revision = $4, image = $5, git_ref = $6,
		 config_hash = $7, cpu = $8, memory_mb = $9, status = $10, note = $11, created_at = $12
		 WHERE id = $1`,
		rel.ID, rel.AppID, rel.OrgID, rel.Revision, rel.Image, rel.GitRef, rel.ConfigHash,
		rel.CPU, rel.MemoryMB, string(rel.Status), rel.Note, rel.CreatedAt,
	)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- Databases ----

func (s *PostgresStore) CreateDatabase(ctx context.Context, d *domain.Database) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO databases (id, org_id, project_id, name, engine, cpu, memory_mb, storage_gb, db_user, db_password, db_name, status, namespace, "release", host, region, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)`,
		d.ID, d.OrgID, d.ProjectID, d.Name, d.Engine, d.CPU, d.MemoryMB, d.StorageGB, d.Username, d.Password, d.DatabaseName, d.Status, d.Namespace, d.Release, d.Host, d.Region, d.CreatedAt,
	)
	return mapErr(err)
}

func (s *PostgresStore) scanDatabase(row pgx.Row) (*domain.Database, error) {
	var d domain.Database
	if err := row.Scan(&d.ID, &d.OrgID, &d.ProjectID, &d.Name, &d.Engine, &d.CPU, &d.MemoryMB, &d.StorageGB, &d.Username, &d.Password, &d.DatabaseName, &d.Status, &d.Namespace, &d.Release, &d.Host, &d.Region, &d.CreatedAt); err != nil {
		return nil, mapErr(err)
	}
	return &d, nil
}

func (s *PostgresStore) GetDatabase(ctx context.Context, id string) (*domain.Database, error) {
	return s.scanDatabase(s.pool.QueryRow(ctx,
		`SELECT id, org_id, project_id, name, engine, cpu, memory_mb, storage_gb, db_user, db_password, db_name, status, namespace, "release", host, region, created_at
		 FROM databases WHERE id = $1`, id))
}

func (s *PostgresStore) ListDatabasesByOrg(ctx context.Context, orgID string) ([]domain.Database, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, org_id, project_id, name, engine, cpu, memory_mb, storage_gb, db_user, db_password, db_name, status, namespace, "release", host, region, created_at
		 FROM databases WHERE org_id = $1`, orgID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make([]domain.Database, 0)
	for rows.Next() {
		d, err := s.scanDatabase(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, mapErr(rows.Err())
}

func (s *PostgresStore) UpdateDatabase(ctx context.Context, d *domain.Database) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE databases SET org_id = $2, project_id = $3, name = $4, engine = $5,
		 cpu = $6, memory_mb = $7, storage_gb = $8, db_user = $9, db_password = $10, db_name = $11,
		 status = $12, namespace = $13, "release" = $14, host = $15, region = $16, created_at = $17 WHERE id = $1`,
		d.ID, d.OrgID, d.ProjectID, d.Name, d.Engine, d.CPU, d.MemoryMB, d.StorageGB, d.Username, d.Password, d.DatabaseName, d.Status, d.Namespace, d.Release, d.Host, d.Region, d.CreatedAt,
	)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) DeleteDatabase(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM databases WHERE id = $1`, id)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- Services ----

func (s *PostgresStore) CreateService(ctx context.Context, svc *domain.Service) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO services (id, org_id, project_id, template, name, cpu, memory_mb, status, namespace, "release", host, region, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		svc.ID, svc.OrgID, svc.ProjectID, svc.Template, svc.Name, svc.CPU, svc.MemoryMB, svc.Status, svc.Namespace, svc.Release, svc.Host, svc.Region, svc.CreatedAt,
	)
	return mapErr(err)
}

func (s *PostgresStore) GetService(ctx context.Context, id string) (*domain.Service, error) {
	var svc domain.Service
	err := s.pool.QueryRow(ctx,
		`SELECT id, org_id, project_id, template, name, cpu, memory_mb, status, namespace, "release", host, region, created_at
		 FROM services WHERE id = $1`, id,
	).Scan(&svc.ID, &svc.OrgID, &svc.ProjectID, &svc.Template, &svc.Name, &svc.CPU, &svc.MemoryMB, &svc.Status, &svc.Namespace, &svc.Release, &svc.Host, &svc.Region, &svc.CreatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &svc, nil
}

func (s *PostgresStore) ListServicesByOrg(ctx context.Context, orgID string) ([]domain.Service, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, org_id, project_id, template, name, cpu, memory_mb, status, namespace, "release", host, region, created_at
		 FROM services WHERE org_id = $1`, orgID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make([]domain.Service, 0)
	for rows.Next() {
		var svc domain.Service
		if err := rows.Scan(&svc.ID, &svc.OrgID, &svc.ProjectID, &svc.Template, &svc.Name, &svc.CPU, &svc.MemoryMB, &svc.Status, &svc.Namespace, &svc.Release, &svc.Host, &svc.Region, &svc.CreatedAt); err != nil {
			return nil, mapErr(err)
		}
		out = append(out, svc)
	}
	return out, mapErr(rows.Err())
}

func (s *PostgresStore) UpdateService(ctx context.Context, svc *domain.Service) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE services SET org_id = $2, project_id = $3, template = $4, name = $5,
		 cpu = $6, memory_mb = $7, status = $8, namespace = $9, "release" = $10, host = $11, region = $12, created_at = $13 WHERE id = $1`,
		svc.ID, svc.OrgID, svc.ProjectID, svc.Template, svc.Name, svc.CPU, svc.MemoryMB, svc.Status, svc.Namespace, svc.Release, svc.Host, svc.Region, svc.CreatedAt,
	)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) DeleteService(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM services WHERE id = $1`, id)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- App environment variables ----

func (s *PostgresStore) GetAppEnv(ctx context.Context, appID string) (map[string]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT key, value FROM app_env WHERE app_id = $1`, appID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, mapErr(err)
		}
		out[k] = v
	}
	return out, mapErr(rows.Err())
}

func (s *PostgresStore) ListAppEnv(ctx context.Context, appID string) ([]domain.AppEnvEntry, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT key, value, secret FROM app_env WHERE app_id = $1 ORDER BY key`, appID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make([]domain.AppEnvEntry, 0)
	for rows.Next() {
		var e domain.AppEnvEntry
		if err := rows.Scan(&e.Key, &e.Value, &e.Secret); err != nil {
			return nil, mapErr(err)
		}
		out = append(out, e)
	}
	return out, mapErr(rows.Err())
}

func (s *PostgresStore) SetAppEnv(ctx context.Context, appID, key, value string, secret bool) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO app_env (app_id, key, value, secret) VALUES ($1, $2, $3, $4)
		 ON CONFLICT (app_id, key) DO UPDATE SET value = EXCLUDED.value, secret = EXCLUDED.secret`,
		appID, key, value, secret,
	)
	return mapErr(err)
}

func (s *PostgresStore) DeleteAppEnv(ctx context.Context, appID, key string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM app_env WHERE app_id = $1 AND key = $2`, appID, key)
	return mapErr(err)
}

// ---- Audit log ----

func (s *PostgresStore) CreateAuditEvent(ctx context.Context, e *domain.AuditEvent) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO audit_events (id, org_id, actor_user_id, actor_email, action, target_type, target_id, metadata, at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		e.ID, e.OrgID, e.ActorUserID, e.ActorEmail, e.Action, e.TargetType, e.TargetID, e.Metadata, e.At,
	)
	return mapErr(err)
}

func (s *PostgresStore) ListAuditEvents(ctx context.Context, f domain.AuditFilter) ([]domain.AuditEvent, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = DefaultPageLimit
	}
	if limit > MaxPageLimit {
		limit = MaxPageLimit
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}
	rows, err := s.pool.Query(ctx,
		`SELECT id, org_id, actor_user_id, actor_email, action, target_type, target_id, metadata, at
		   FROM audit_events WHERE org_id = $1 ORDER BY at DESC, id DESC LIMIT $2 OFFSET $3`, f.OrgID, limit, offset)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make([]domain.AuditEvent, 0)
	for rows.Next() {
		var e domain.AuditEvent
		if err := rows.Scan(&e.ID, &e.OrgID, &e.ActorUserID, &e.ActorEmail,
			&e.Action, &e.TargetType, &e.TargetID, &e.Metadata, &e.At); err != nil {
			return nil, mapErr(err)
		}
		out = append(out, e)
	}
	return out, mapErr(rows.Err())
}

// CountAuditEvents returns the total number of events matching the filter's scope
// (OrgID), ignoring Limit/Offset, so the list endpoint can report has-more.
func (s *PostgresStore) CountAuditEvents(ctx context.Context, f domain.AuditFilter) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_events WHERE org_id = $1`, f.OrgID).Scan(&n); err != nil {
		return 0, mapErr(err)
	}
	return n, nil
}

// ---- Domains ----

func (s *PostgresStore) CreateDomain(ctx context.Context, d *domain.Domain) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO domains (id, org_id, app_id, domain, verified, status, verification_token, verified_at, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		d.ID, d.OrgID, d.AppID, d.Domain, d.Verified, string(d.Status), d.VerificationToken, nullTime(d.VerifiedAt), d.CreatedAt,
	)
	return mapErr(err)
}

// scanDomain scans a domains row (with nullable verified_at) into a Domain.
func scanDomain(row interface{ Scan(...any) error }) (domain.Domain, error) {
	var d domain.Domain
	var status string
	var verifiedAt *time.Time
	if err := row.Scan(&d.ID, &d.OrgID, &d.AppID, &d.Domain, &d.Verified, &status, &d.VerificationToken, &verifiedAt, &d.CreatedAt); err != nil {
		return domain.Domain{}, err
	}
	d.Status = domain.DomainStatus(status)
	if verifiedAt != nil {
		d.VerifiedAt = *verifiedAt
	}
	return d, nil
}

func (s *PostgresStore) GetDomain(ctx context.Context, id string) (*domain.Domain, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT id, org_id, app_id, domain, verified, status, verification_token, verified_at, created_at FROM domains WHERE id = $1`, id)
	d, err := scanDomain(row)
	if err != nil {
		return nil, mapErr(err)
	}
	return &d, nil
}

func (s *PostgresStore) ListDomainsByApp(ctx context.Context, appID string) ([]domain.Domain, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, org_id, app_id, domain, verified, status, verification_token, verified_at, created_at FROM domains WHERE app_id = $1`, appID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make([]domain.Domain, 0)
	for rows.Next() {
		d, err := scanDomain(rows)
		if err != nil {
			return nil, mapErr(err)
		}
		out = append(out, d)
	}
	return out, mapErr(rows.Err())
}

// GetVerifiedDomainByHost returns the single VERIFIED domain owning host
// (case-insensitive, status='verified'), regardless of app/org. The DB partial
// unique index (domains_verified_host_uniq) guarantees at most one such row.
// Returns ErrNotFound when no verified row owns the host.
func (s *PostgresStore) GetVerifiedDomainByHost(ctx context.Context, host string) (*domain.Domain, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT id, org_id, app_id, domain, verified, status, verification_token, verified_at, created_at
		 FROM domains WHERE lower(domain) = lower($1) AND status = 'verified' LIMIT 1`, host)
	d, err := scanDomain(row)
	if err != nil {
		return nil, mapErr(err)
	}
	return &d, nil
}

func (s *PostgresStore) UpdateDomain(ctx context.Context, d *domain.Domain) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE domains SET domain = $2, verified = $3, status = $4, verification_token = $5, verified_at = $6 WHERE id = $1`,
		d.ID, d.Domain, d.Verified, string(d.Status), d.VerificationToken, nullTime(d.VerifiedAt),
	)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) DeleteDomain(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM domains WHERE id = $1`, id)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- Projects ----

func (s *PostgresStore) CreateProject(ctx context.Context, p *domain.Project) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO projects (id, org_id, name, slug, is_default, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		p.ID, p.OrgID, p.Name, p.Slug, p.IsDefault, p.CreatedAt,
	)
	return mapErr(err)
}

func (s *PostgresStore) GetProject(ctx context.Context, id string) (*domain.Project, error) {
	var p domain.Project
	err := s.pool.QueryRow(ctx,
		`SELECT id, org_id, name, slug, is_default, created_at FROM projects WHERE id = $1`, id,
	).Scan(&p.ID, &p.OrgID, &p.Name, &p.Slug, &p.IsDefault, &p.CreatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &p, nil
}

func (s *PostgresStore) ListProjectsByOrg(ctx context.Context, orgID string) ([]domain.Project, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, org_id, name, slug, is_default, created_at FROM projects WHERE org_id = $1`, orgID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make([]domain.Project, 0)
	for rows.Next() {
		var p domain.Project
		if err := rows.Scan(&p.ID, &p.OrgID, &p.Name, &p.Slug, &p.IsDefault, &p.CreatedAt); err != nil {
			return nil, mapErr(err)
		}
		out = append(out, p)
	}
	return out, mapErr(rows.Err())
}

// DeleteProject removes an empty project scoped to orgID. It returns ErrNotFound
// when the project does not exist within the org, and ErrConflict when the
// project still owns any apps or services.
func (s *PostgresStore) DeleteProject(ctx context.Context, orgID, projectID string) error {
	var exists bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM projects WHERE id = $1 AND org_id = $2)`,
		projectID, orgID,
	).Scan(&exists); err != nil {
		return mapErr(err)
	}
	if !exists {
		return ErrNotFound
	}

	var inUse bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM apps WHERE project_id = $1)
		    OR EXISTS (SELECT 1 FROM services WHERE project_id = $1)`,
		projectID,
	).Scan(&inUse); err != nil {
		return mapErr(err)
	}
	if inUse {
		return ErrConflict
	}

	tag, err := s.pool.Exec(ctx,
		`DELETE FROM projects WHERE id = $1 AND org_id = $2`, projectID, orgID)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- Project memberships ----

func (s *PostgresStore) AddProjectMembership(ctx context.Context, m domain.ProjectMembership) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO project_memberships (project_id, user_id, role) VALUES ($1, $2, $3)`,
		m.ProjectID, m.UserID, string(m.Role),
	)
	return mapErr(err)
}

func (s *PostgresStore) GetProjectMembership(ctx context.Context, projectID, userID string) (*domain.ProjectMembership, error) {
	var m domain.ProjectMembership
	var role string
	err := s.pool.QueryRow(ctx,
		`SELECT project_id, user_id, role FROM project_memberships WHERE project_id = $1 AND user_id = $2`,
		projectID, userID,
	).Scan(&m.ProjectID, &m.UserID, &role)
	if err != nil {
		return nil, mapErr(err)
	}
	m.Role = domain.Role(role)
	return &m, nil
}

// ---- Invitations ----

func (s *PostgresStore) CreateInvitation(ctx context.Context, inv *domain.Invitation) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO invitations (id, org_id, project_id, email, role, token, status, invited_by, created_at, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		inv.ID, inv.OrgID, inv.ProjectID, inv.Email, string(inv.Role), inv.Token, string(inv.Status), inv.InvitedBy, inv.CreatedAt, nullTime(inv.ExpiresAt),
	)
	return mapErr(err)
}

func (s *PostgresStore) GetInvitationByToken(ctx context.Context, token string) (*domain.Invitation, error) {
	return s.scanInvitation(s.pool.QueryRow(ctx,
		`SELECT id, org_id, project_id, email, role, token, status, invited_by, created_at, expires_at
		 FROM invitations WHERE token = $1`, token))
}

func (s *PostgresStore) scanInvitation(row pgx.Row) (*domain.Invitation, error) {
	var inv domain.Invitation
	var role, status string
	var expires *time.Time
	if err := row.Scan(&inv.ID, &inv.OrgID, &inv.ProjectID, &inv.Email, &role, &inv.Token, &status, &inv.InvitedBy, &inv.CreatedAt, &expires); err != nil {
		return nil, mapErr(err)
	}
	inv.Role = domain.Role(role)
	inv.Status = domain.InvitationStatus(status)
	if expires != nil {
		inv.ExpiresAt = *expires
	}
	return &inv, nil
}

func (s *PostgresStore) ListInvitationsByOrg(ctx context.Context, orgID string) ([]domain.Invitation, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, org_id, project_id, email, role, token, status, invited_by, created_at, expires_at
		 FROM invitations WHERE org_id = $1`, orgID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make([]domain.Invitation, 0)
	for rows.Next() {
		inv, err := s.scanInvitation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *inv)
	}
	return out, mapErr(rows.Err())
}

func (s *PostgresStore) UpdateInvitation(ctx context.Context, inv *domain.Invitation) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE invitations SET org_id = $2, project_id = $3, email = $4, role = $5, token = $6,
		 status = $7, invited_by = $8, created_at = $9, expires_at = $10 WHERE id = $1`,
		inv.ID, inv.OrgID, inv.ProjectID, inv.Email, string(inv.Role), inv.Token, string(inv.Status), inv.InvitedBy, inv.CreatedAt, nullTime(inv.ExpiresAt),
	)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RevokeInvitation marks an org's invitation as revoked. It returns ErrNotFound
// when no matching invitation exists within the org.
func (s *PostgresStore) RevokeInvitation(ctx context.Context, orgID, inviteID string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE invitations SET status = $3 WHERE id = $1 AND org_id = $2`,
		inviteID, orgID, string(domain.InviteRevoked),
	)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- Refresh tokens (rotation + revocation) ----

func (s *PostgresStore) CreateRefreshToken(ctx context.Context, rt *domain.RefreshToken) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO refresh_tokens (id, user_id, revoked, created_at, expires_at) VALUES ($1, $2, $3, $4, $5)`,
		rt.ID, rt.UserID, rt.Revoked, rt.CreatedAt, nullTime(rt.ExpiresAt),
	)
	return mapErr(err)
}

func (s *PostgresStore) GetRefreshToken(ctx context.Context, id string) (*domain.RefreshToken, error) {
	var rt domain.RefreshToken
	var expires *time.Time
	err := s.pool.QueryRow(ctx,
		`SELECT id, user_id, revoked, created_at, expires_at FROM refresh_tokens WHERE id = $1`, id,
	).Scan(&rt.ID, &rt.UserID, &rt.Revoked, &rt.CreatedAt, &expires)
	if err != nil {
		return nil, mapErr(err)
	}
	if expires != nil {
		rt.ExpiresAt = *expires
	}
	return &rt, nil
}

func (s *PostgresStore) RevokeRefreshToken(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE refresh_tokens SET revoked = true WHERE id = $1`, id)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RevokeRefreshTokenIfActive performs the conditional UPDATE that makes rotation
// race-free: it revokes the row only WHERE revoked = false. RowsAffected == 1
// means this call won the rotation; 0 means the row was already revoked (a lost
// race or a replay). It distinguishes "already revoked" from "missing" with a
// follow-up existence check so the caller can treat a replay (exists+revoked)
// differently from an unknown jti.
func (s *PostgresStore) RevokeRefreshTokenIfActive(ctx context.Context, id string) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE refresh_tokens SET revoked = true WHERE id = $1 AND revoked = false`, id)
	if err != nil {
		return false, mapErr(err)
	}
	if tag.RowsAffected() == 1 {
		return true, nil
	}
	// 0 rows: either the row does not exist (ErrNotFound) or it was already revoked.
	var exists bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM refresh_tokens WHERE id = $1)`, id,
	).Scan(&exists); err != nil {
		return false, mapErr(err)
	}
	if !exists {
		return false, ErrNotFound
	}
	return false, nil // exists but already revoked
}

func (s *PostgresStore) RevokeAllUserRefreshTokens(ctx context.Context, userID string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE refresh_tokens SET revoked = true WHERE user_id = $1 AND revoked = false`, userID)
	return mapErr(err)
}

// DeleteExpiredRefreshTokens removes ONLY truly-expired rows (expires_at < before),
// returning the number deleted. It deliberately does NOT purge revoked-but-unexpired
// rows: a revoked row is the replay-detection TOMBSTONE a rotated token needs. If a
// stolen token is replayed after the next cleanup tick, Refresh must still find the
// revoked record (rec.Revoked == true) so it can kill the whole family. A revoked
// tombstone therefore survives until its underlying JWT can no longer Verify
// (expires_at passes), at which point a replay can no longer reach this code anyway.
func (s *PostgresStore) DeleteExpiredRefreshTokens(ctx context.Context, before time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM refresh_tokens WHERE expires_at IS NOT NULL AND expires_at < $1`, before)
	if err != nil {
		return 0, mapErr(err)
	}
	return tag.RowsAffected(), nil
}

// ---- Password reset tokens (single-use, time-limited, hashed at rest) ----

func (s *PostgresStore) CreatePasswordResetToken(ctx context.Context, t *domain.PasswordResetToken) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO password_reset_tokens (id, user_id, token_hash, expires_at, used_at, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		t.ID, t.UserID, t.TokenHash, t.ExpiresAt, nullTime(t.UsedAt), t.CreatedAt,
	)
	return mapErr(err)
}

// InvalidateUserPasswordResetTokens marks all of a user's currently-unused reset
// tokens used, so a newly issued token is the only live one for that user.
func (s *PostgresStore) InvalidateUserPasswordResetTokens(ctx context.Context, userID string, usedAt time.Time) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE password_reset_tokens SET used_at = $2 WHERE user_id = $1 AND used_at IS NULL`, userID, usedAt)
	return mapErr(err)
}

func (s *PostgresStore) GetPasswordResetTokenByHash(ctx context.Context, tokenHash string) (*domain.PasswordResetToken, error) {
	var t domain.PasswordResetToken
	var usedAt *time.Time
	err := s.pool.QueryRow(ctx,
		`SELECT id, user_id, token_hash, expires_at, used_at, created_at
		 FROM password_reset_tokens WHERE token_hash = $1`, tokenHash,
	).Scan(&t.ID, &t.UserID, &t.TokenHash, &t.ExpiresAt, &usedAt, &t.CreatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	if usedAt != nil {
		t.UsedAt = *usedAt
	}
	return &t, nil
}

// ConsumePasswordResetToken atomically marks the token used WHERE used_at IS NULL.
// RowsAffected == 1 means this call consumed it; 0 means it was already used (or
// missing — distinguished with an existence check).
func (s *PostgresStore) ConsumePasswordResetToken(ctx context.Context, id string, usedAt time.Time) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE password_reset_tokens SET used_at = $2 WHERE id = $1 AND used_at IS NULL`, id, usedAt)
	if err != nil {
		return false, mapErr(err)
	}
	if tag.RowsAffected() == 1 {
		return true, nil
	}
	var exists bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM password_reset_tokens WHERE id = $1)`, id,
	).Scan(&exists); err != nil {
		return false, mapErr(err)
	}
	if !exists {
		return false, ErrNotFound
	}
	return false, nil // exists but already used
}

// nullTime maps a zero time.Time to NULL so optional timestamp columns store NULL
// rather than the Go zero value (0001-01-01), which would otherwise read back as a
// real, far-past instant.
func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// ---- API tokens (personal access tokens; hashed at rest) ----

func (s *PostgresStore) CreateApiToken(ctx context.Context, t *domain.ApiToken) error {
	scopes := t.Scopes
	if scopes == nil {
		scopes = []string{}
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO api_tokens (id, user_id, name, token_hash, prefix, scopes, expires_at, last_used_at, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		t.ID, t.UserID, t.Name, t.TokenHash, t.Prefix, scopes,
		nullTime(t.ExpiresAt), nullTime(t.LastUsedAt), t.CreatedAt,
	)
	return mapErr(err)
}

func (s *PostgresStore) scanApiToken(row pgx.Row) (*domain.ApiToken, error) {
	var t domain.ApiToken
	var expiresAt, lastUsedAt *time.Time
	if err := row.Scan(&t.ID, &t.UserID, &t.Name, &t.TokenHash, &t.Prefix,
		&t.Scopes, &expiresAt, &lastUsedAt, &t.CreatedAt); err != nil {
		return nil, mapErr(err)
	}
	if expiresAt != nil {
		t.ExpiresAt = *expiresAt
	}
	if lastUsedAt != nil {
		t.LastUsedAt = *lastUsedAt
	}
	return &t, nil
}

func (s *PostgresStore) GetApiTokenByHash(ctx context.Context, tokenHash string) (*domain.ApiToken, error) {
	return s.scanApiToken(s.pool.QueryRow(ctx,
		`SELECT id, user_id, name, token_hash, prefix, scopes, expires_at, last_used_at, created_at
		 FROM api_tokens WHERE token_hash = $1`, tokenHash))
}

func (s *PostgresStore) ListApiTokensByUser(ctx context.Context, userID string) ([]domain.ApiToken, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, user_id, name, token_hash, prefix, scopes, expires_at, last_used_at, created_at
		 FROM api_tokens WHERE user_id = $1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var out []domain.ApiToken
	for rows.Next() {
		t, err := s.scanApiToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, mapErr(rows.Err())
}

// DeleteApiToken removes a token scoped to its owner, so one user can never
// delete another user's token by id. ErrNotFound when no matching row exists.
func (s *PostgresStore) DeleteApiToken(ctx context.Context, userID, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM api_tokens WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) TouchApiToken(ctx context.Context, id string, lastUsedAt time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE api_tokens SET last_used_at = $2 WHERE id = $1`, id, lastUsedAt)
	return mapErr(err)
}

// ---- Billing: subscriptions & usage ----

func (s *PostgresStore) UpsertSubscription(ctx context.Context, sub *domain.Subscription) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO subscriptions (org_id, plan_id, status, stripe_customer_id, stripe_subscription_id, stripe_subscription_item_id, created_at, current_period_end)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 ON CONFLICT (org_id) DO UPDATE SET
		   plan_id = EXCLUDED.plan_id,
		   status = EXCLUDED.status,
		   stripe_customer_id = EXCLUDED.stripe_customer_id,
		   stripe_subscription_id = EXCLUDED.stripe_subscription_id,
		   stripe_subscription_item_id = EXCLUDED.stripe_subscription_item_id,
		   created_at = EXCLUDED.created_at,
		   current_period_end = EXCLUDED.current_period_end`,
		sub.OrgID, sub.PlanID, string(sub.Status), sub.StripeCustomerID, sub.StripeSubscriptionID, sub.StripeSubscriptionItemID, sub.CreatedAt, sub.CurrentPeriodEnd,
	)
	return mapErr(err)
}

func (s *PostgresStore) GetSubscription(ctx context.Context, orgID string) (*domain.Subscription, error) {
	var sub domain.Subscription
	var status string
	err := s.pool.QueryRow(ctx,
		`SELECT org_id, plan_id, status, stripe_customer_id, stripe_subscription_id, stripe_subscription_item_id, created_at, current_period_end
		 FROM subscriptions WHERE org_id = $1`, orgID,
	).Scan(&sub.OrgID, &sub.PlanID, &status, &sub.StripeCustomerID, &sub.StripeSubscriptionID, &sub.StripeSubscriptionItemID, &sub.CreatedAt, &sub.CurrentPeriodEnd)
	if err != nil {
		return nil, mapErr(err)
	}
	sub.Status = domain.SubscriptionStatus(status)
	return &sub, nil
}

func (s *PostgresStore) AddUsage(ctx context.Context, u *domain.UsageRecord) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO usage_records (id, org_id, metric, quantity, at) VALUES ($1, $2, $3, $4, $5)`,
		u.ID, u.OrgID, u.Metric, u.Quantity, u.At,
	)
	return mapErr(err)
}

// AddUsageIfAbsent inserts the record keyed by its deterministic id, ATOMICALLY:
// ON CONFLICT (id) DO NOTHING makes the per-(org,hour) write race-free across
// restarts, replicas and concurrent ticks. RowsAffected reports inserted=true on
// the first write and false on a duplicate id (no error).
func (s *PostgresStore) AddUsageIfAbsent(ctx context.Context, u *domain.UsageRecord) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		`INSERT INTO usage_records (id, org_id, metric, quantity, at) VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (id) DO NOTHING`,
		u.ID, u.OrgID, u.Metric, u.Quantity, u.At,
	)
	if err != nil {
		return false, mapErr(err)
	}
	return tag.RowsAffected() == 1, nil
}

func (s *PostgresStore) ListUsageByOrg(ctx context.Context, orgID string, p Page) ([]domain.UsageRecord, error) {
	sql := `SELECT id, org_id, metric, quantity, at FROM usage_records WHERE org_id = $1 ORDER BY at DESC, id DESC`
	args := []any{orgID}
	sql, args = appendPage(sql, args, p)
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make([]domain.UsageRecord, 0)
	for rows.Next() {
		var u domain.UsageRecord
		if err := rows.Scan(&u.ID, &u.OrgID, &u.Metric, &u.Quantity, &u.At); err != nil {
			return nil, mapErr(err)
		}
		out = append(out, u)
	}
	return out, mapErr(rows.Err())
}

func (s *PostgresStore) ListUsageByOrgSince(ctx context.Context, orgID string, since time.Time, p Page) ([]domain.UsageRecord, error) {
	sql := `SELECT id, org_id, metric, quantity, at FROM usage_records WHERE org_id = $1 AND at >= $2 ORDER BY at DESC, id DESC`
	args := []any{orgID, since}
	sql, args = appendPage(sql, args, p)
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make([]domain.UsageRecord, 0)
	for rows.Next() {
		var u domain.UsageRecord
		if err := rows.Scan(&u.ID, &u.OrgID, &u.Metric, &u.Quantity, &u.At); err != nil {
			return nil, mapErr(err)
		}
		out = append(out, u)
	}
	return out, mapErr(rows.Err())
}

func (s *PostgresStore) GetSubscriptionByStripeID(ctx context.Context, stripeSubID string) (*domain.Subscription, error) {
	if stripeSubID == "" {
		return nil, ErrNotFound
	}
	var sub domain.Subscription
	var status string
	err := s.pool.QueryRow(ctx,
		`SELECT org_id, plan_id, status, stripe_customer_id, stripe_subscription_id, stripe_subscription_item_id, created_at, current_period_end
		 FROM subscriptions WHERE stripe_subscription_id = $1`, stripeSubID,
	).Scan(&sub.OrgID, &sub.PlanID, &status, &sub.StripeCustomerID, &sub.StripeSubscriptionID, &sub.StripeSubscriptionItemID, &sub.CreatedAt, &sub.CurrentPeriodEnd)
	if err != nil {
		return nil, mapErr(err)
	}
	sub.Status = domain.SubscriptionStatus(status)
	return &sub, nil
}

func (s *PostgresStore) GetSubscriptionByCustomerID(ctx context.Context, customerID string) (*domain.Subscription, error) {
	if customerID == "" {
		return nil, ErrNotFound
	}
	var sub domain.Subscription
	var status string
	err := s.pool.QueryRow(ctx,
		`SELECT org_id, plan_id, status, stripe_customer_id, stripe_subscription_id, stripe_subscription_item_id, created_at, current_period_end
		 FROM subscriptions WHERE stripe_customer_id = $1`, customerID,
	).Scan(&sub.OrgID, &sub.PlanID, &status, &sub.StripeCustomerID, &sub.StripeSubscriptionID, &sub.StripeSubscriptionItemID, &sub.CreatedAt, &sub.CurrentPeriodEnd)
	if err != nil {
		return nil, mapErr(err)
	}
	sub.Status = domain.SubscriptionStatus(status)
	return &sub, nil
}

// EventProcessed reports whether a Stripe event id has already been recorded. It
// is a read-only peek used by the webhook fast path; the authoritative record
// happens via MarkEventProcessed only after a successful apply.
func (s *PostgresStore) EventProcessed(ctx context.Context, eventID string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM processed_stripe_events WHERE event_id = $1)`, eventID,
	).Scan(&exists)
	if err != nil {
		return false, mapErr(err)
	}
	return exists, nil
}

// MarkEventProcessed inserts a Stripe event id, reporting whether it was newly
// inserted. ON CONFLICT DO NOTHING + RowsAffected gives an atomic, race-free
// dedupe even across concurrent webhook replicas.
func (s *PostgresStore) MarkEventProcessed(ctx context.Context, eventID string) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		`INSERT INTO processed_stripe_events (event_id) VALUES ($1) ON CONFLICT (event_id) DO NOTHING`, eventID)
	if err != nil {
		return false, mapErr(err)
	}
	return tag.RowsAffected() == 1, nil
}

func (s *PostgresStore) GetMeterState(ctx context.Context) (*domain.MeterState, error) {
	var st domain.MeterState
	err := s.pool.QueryRow(ctx,
		`SELECT last_metered_hour FROM meter_state WHERE id = true`).Scan(&st.LastMeteredHour)
	if err != nil {
		return nil, mapErr(err)
	}
	return &st, nil
}

func (s *PostgresStore) SetMeterState(ctx context.Context, st *domain.MeterState) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO meter_state (id, last_metered_hour) VALUES (true, $1)
		 ON CONFLICT (id) DO UPDATE SET last_metered_hour = EXCLUDED.last_metered_hour`,
		st.LastMeteredHour)
	return mapErr(err)
}

func (s *PostgresStore) GetUsageReportState(ctx context.Context, orgID string) (*domain.UsageReportState, error) {
	st := domain.UsageReportState{OrgID: orgID}
	err := s.pool.QueryRow(ctx,
		`SELECT period_start, reported_cents FROM usage_report_state WHERE org_id = $1`,
		orgID).Scan(&st.PeriodStart, &st.ReportedCents)
	if err != nil {
		return nil, mapErr(err)
	}
	return &st, nil
}

func (s *PostgresStore) SetUsageReportState(ctx context.Context, st *domain.UsageReportState) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO usage_report_state (org_id, period_start, reported_cents) VALUES ($1, $2, $3)
		 ON CONFLICT (org_id) DO UPDATE SET period_start = EXCLUDED.period_start, reported_cents = EXCLUDED.reported_cents`,
		st.OrgID, st.PeriodStart, st.ReportedCents)
	return mapErr(err)
}

// ---- Plans ----

func (s *PostgresStore) ListPlans(ctx context.Context) ([]domain.Plan, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, name, description, price_cents, currency, included_hours, overage_per_hour_cents,
		 stripe_price_id, max_cpu, max_memory_mb, max_apps, is_default, sort_order, active FROM plans`)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make([]domain.Plan, 0)
	for rows.Next() {
		p, err := scanPlanRow(rows)
		if err != nil {
			return nil, mapErr(err)
		}
		out = append(out, p)
	}
	return out, mapErr(rows.Err())
}

func (s *PostgresStore) GetPlan(ctx context.Context, id string) (*domain.Plan, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT id, name, description, price_cents, currency, included_hours, overage_per_hour_cents,
		 stripe_price_id, max_cpu, max_memory_mb, max_apps, is_default, sort_order, active FROM plans WHERE id = $1`, id)
	p, err := scanPlanRow(row)
	if err != nil {
		return nil, mapErr(err)
	}
	return &p, nil
}

// scanDest abstracts pgx.Row and pgx.Rows (both have Scan).
type scanDest interface {
	Scan(dest ...any) error
}

func scanPlanRow(row scanDest) (domain.Plan, error) {
	var p domain.Plan
	err := row.Scan(&p.ID, &p.Name, &p.Description, &p.PriceCents, &p.Currency, &p.IncludedHours,
		&p.OveragePerHourCents, &p.StripePriceID, &p.MaxCPU, &p.MaxMemoryMB, &p.MaxApps, &p.IsDefault, &p.SortOrder, &p.Active)
	return p, err
}

func (s *PostgresStore) UpsertPlan(ctx context.Context, p *domain.Plan) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO plans (id, name, description, price_cents, currency, included_hours, overage_per_hour_cents,
		 stripe_price_id, max_cpu, max_memory_mb, max_apps, is_default, sort_order, active)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		 ON CONFLICT (id) DO UPDATE SET
		   name = EXCLUDED.name,
		   description = EXCLUDED.description,
		   price_cents = EXCLUDED.price_cents,
		   currency = EXCLUDED.currency,
		   included_hours = EXCLUDED.included_hours,
		   overage_per_hour_cents = EXCLUDED.overage_per_hour_cents,
		   stripe_price_id = EXCLUDED.stripe_price_id,
		   max_cpu = EXCLUDED.max_cpu,
		   max_memory_mb = EXCLUDED.max_memory_mb,
		   max_apps = EXCLUDED.max_apps,
		   is_default = EXCLUDED.is_default,
		   sort_order = EXCLUDED.sort_order,
		   active = EXCLUDED.active`,
		p.ID, p.Name, p.Description, p.PriceCents, p.Currency, p.IncludedHours, p.OveragePerHourCents,
		p.StripePriceID, p.MaxCPU, p.MaxMemoryMB, p.MaxApps, p.IsDefault, p.SortOrder, p.Active,
	)
	return mapErr(err)
}

func (s *PostgresStore) DeletePlan(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM plans WHERE id = $1`, id)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- Pricing components ----

func scanPricingRow(row scanDest) (domain.PricingComponent, error) {
	var p domain.PricingComponent
	err := row.Scan(&p.Key, &p.Name, &p.Unit, &p.PricePerHour, &p.Currency, &p.Active, &p.SortOrder)
	return p, err
}

func (s *PostgresStore) ListPricingComponents(ctx context.Context) ([]domain.PricingComponent, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT key, name, unit, price_per_hour, currency, active, sort_order FROM pricing_components`)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make([]domain.PricingComponent, 0)
	for rows.Next() {
		p, err := scanPricingRow(rows)
		if err != nil {
			return nil, mapErr(err)
		}
		out = append(out, p)
	}
	return out, mapErr(rows.Err())
}

func (s *PostgresStore) GetPricingComponent(ctx context.Context, key string) (*domain.PricingComponent, error) {
	p, err := scanPricingRow(s.pool.QueryRow(ctx,
		`SELECT key, name, unit, price_per_hour, currency, active, sort_order FROM pricing_components WHERE key = $1`, key))
	if err != nil {
		return nil, mapErr(err)
	}
	return &p, nil
}

func (s *PostgresStore) UpsertPricingComponent(ctx context.Context, p *domain.PricingComponent) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO pricing_components (key, name, unit, price_per_hour, currency, active, sort_order)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 ON CONFLICT (key) DO UPDATE SET
		   name = EXCLUDED.name,
		   unit = EXCLUDED.unit,
		   price_per_hour = EXCLUDED.price_per_hour,
		   currency = EXCLUDED.currency,
		   active = EXCLUDED.active,
		   sort_order = EXCLUDED.sort_order`,
		p.Key, p.Name, p.Unit, p.PricePerHour, p.Currency, p.Active, p.SortOrder,
	)
	return mapErr(err)
}

func (s *PostgresStore) DeletePricingComponent(ctx context.Context, key string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM pricing_components WHERE key = $1`, key)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- Service templates ----

func (s *PostgresStore) ListServiceTemplates(ctx context.Context) ([]domain.ServiceTemplate, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT key, name, description, category, kind, image, default_port, active, sort_order FROM service_templates`)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make([]domain.ServiceTemplate, 0)
	for rows.Next() {
		var t domain.ServiceTemplate
		if err := rows.Scan(&t.Key, &t.Name, &t.Description, &t.Category, &t.Kind, &t.Image, &t.DefaultPort, &t.Active, &t.SortOrder); err != nil {
			return nil, mapErr(err)
		}
		out = append(out, t)
	}
	return out, mapErr(rows.Err())
}

func (s *PostgresStore) GetServiceTemplate(ctx context.Context, key string) (*domain.ServiceTemplate, error) {
	var t domain.ServiceTemplate
	err := s.pool.QueryRow(ctx,
		`SELECT key, name, description, category, kind, image, default_port, active, sort_order
		 FROM service_templates WHERE key = $1`, key,
	).Scan(&t.Key, &t.Name, &t.Description, &t.Category, &t.Kind, &t.Image, &t.DefaultPort, &t.Active, &t.SortOrder)
	if err != nil {
		return nil, mapErr(err)
	}
	return &t, nil
}

func (s *PostgresStore) UpsertServiceTemplate(ctx context.Context, t *domain.ServiceTemplate) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO service_templates (key, name, description, category, kind, image, default_port, active, sort_order)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 ON CONFLICT (key) DO UPDATE SET
		   name = EXCLUDED.name,
		   description = EXCLUDED.description,
		   category = EXCLUDED.category,
		   kind = EXCLUDED.kind,
		   image = EXCLUDED.image,
		   default_port = EXCLUDED.default_port,
		   active = EXCLUDED.active,
		   sort_order = EXCLUDED.sort_order`,
		t.Key, t.Name, t.Description, t.Category, t.Kind, t.Image, t.DefaultPort, t.Active, t.SortOrder,
	)
	return mapErr(err)
}

func (s *PostgresStore) DeleteServiceTemplate(ctx context.Context, key string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM service_templates WHERE key = $1`, key)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- Platform settings (singleton) ----

func (s *PostgresStore) GetSettings(ctx context.Context) (*domain.PlatformSettings, error) {
	var ps domain.PlatformSettings
	err := s.pool.QueryRow(ctx,
		`SELECT default_cpu, default_memory_mb, default_plan_id, cpu_overcommit_factor,
		 memory_overcommit_factor, default_region, regions, grace_past_due, default_spend_cap_cents,
		 keda_default_min_replicas, keda_default_max_replicas, keda_polling_interval,
		 keda_cooldown_period, keda_cpu_utilization, keda_http_trigger, keda_max_replicas_ceiling
		 FROM platform_settings WHERE id = true`,
	).Scan(&ps.DefaultCPU, &ps.DefaultMemoryMB, &ps.DefaultPlanID, &ps.CPUOvercommitFactor,
		&ps.MemoryOvercommitFactor, &ps.DefaultRegion, &ps.Regions, &ps.GracePastDue, &ps.DefaultSpendCapCents,
		&ps.KedaDefaultMinReplicas, &ps.KedaDefaultMaxReplicas, &ps.KedaPollingInterval,
		&ps.KedaCooldownPeriod, &ps.KedaCPUUtilization, &ps.KedaHTTPTrigger, &ps.KedaMaxReplicasCeiling)
	if err != nil {
		return nil, mapErr(err)
	}
	return &ps, nil
}

func (s *PostgresStore) UpdateSettings(ctx context.Context, in *domain.PlatformSettings) error {
	regions := in.Regions
	if regions == nil {
		regions = []string{}
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO platform_settings (id, default_cpu, default_memory_mb, default_plan_id,
		 cpu_overcommit_factor, memory_overcommit_factor, default_region, regions,
		 grace_past_due, default_spend_cap_cents,
		 keda_default_min_replicas, keda_default_max_replicas, keda_polling_interval,
		 keda_cooldown_period, keda_cpu_utilization, keda_http_trigger, keda_max_replicas_ceiling)
		 VALUES (true, $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		 ON CONFLICT (id) DO UPDATE SET
		   default_cpu = EXCLUDED.default_cpu,
		   default_memory_mb = EXCLUDED.default_memory_mb,
		   default_plan_id = EXCLUDED.default_plan_id,
		   cpu_overcommit_factor = EXCLUDED.cpu_overcommit_factor,
		   memory_overcommit_factor = EXCLUDED.memory_overcommit_factor,
		   default_region = EXCLUDED.default_region,
		   regions = EXCLUDED.regions,
		   grace_past_due = EXCLUDED.grace_past_due,
		   default_spend_cap_cents = EXCLUDED.default_spend_cap_cents,
		   keda_default_min_replicas = EXCLUDED.keda_default_min_replicas,
		   keda_default_max_replicas = EXCLUDED.keda_default_max_replicas,
		   keda_polling_interval = EXCLUDED.keda_polling_interval,
		   keda_cooldown_period = EXCLUDED.keda_cooldown_period,
		   keda_cpu_utilization = EXCLUDED.keda_cpu_utilization,
		   keda_http_trigger = EXCLUDED.keda_http_trigger,
		   keda_max_replicas_ceiling = EXCLUDED.keda_max_replicas_ceiling`,
		in.DefaultCPU, in.DefaultMemoryMB, in.DefaultPlanID, in.CPUOvercommitFactor,
		in.MemoryOvercommitFactor, in.DefaultRegion, regions, in.GracePastDue, in.DefaultSpendCapCents,
		in.KedaDefaultMinReplicas, in.KedaDefaultMaxReplicas, in.KedaPollingInterval,
		in.KedaCooldownPeriod, in.KedaCPUUtilization, in.KedaHTTPTrigger, in.KedaMaxReplicasCeiling,
	)
	return mapErr(err)
}

// ---- Admin overview helpers ----

func (s *PostgresStore) ListAllOrgs(ctx context.Context) ([]domain.Organization, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, name, slug, billing_email, spend_cap_cents, created_at FROM organizations`)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make([]domain.Organization, 0)
	for rows.Next() {
		var o domain.Organization
		if err := rows.Scan(&o.ID, &o.Name, &o.Slug, &o.BillingEmail, &o.SpendCapCents, &o.CreatedAt); err != nil {
			return nil, mapErr(err)
		}
		out = append(out, o)
	}
	return out, mapErr(rows.Err())
}

func (s *PostgresStore) CountUsers(ctx context.Context) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
		return 0, mapErr(err)
	}
	return n, nil
}

func (s *PostgresStore) ListAllSubscriptions(ctx context.Context) ([]domain.Subscription, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT org_id, plan_id, status, stripe_customer_id, stripe_subscription_id, stripe_subscription_item_id, created_at, current_period_end
		 FROM subscriptions`)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make([]domain.Subscription, 0)
	for rows.Next() {
		var sub domain.Subscription
		var status string
		if err := rows.Scan(&sub.OrgID, &sub.PlanID, &status, &sub.StripeCustomerID, &sub.StripeSubscriptionID, &sub.StripeSubscriptionItemID, &sub.CreatedAt, &sub.CurrentPeriodEnd); err != nil {
			return nil, mapErr(err)
		}
		sub.Status = domain.SubscriptionStatus(status)
		out = append(out, sub)
	}
	return out, mapErr(rows.Err())
}

// SumUsageByMetric aggregates total quantity per metric in SQL, avoiding an
// unbounded full-table scan-and-sum in Go.
func (s *PostgresStore) SumUsageByMetric(ctx context.Context) (map[string]int64, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT metric, sum(quantity) FROM usage_records GROUP BY metric`)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make(map[string]int64)
	for rows.Next() {
		var metric string
		var total int64
		if err := rows.Scan(&metric, &total); err != nil {
			return nil, mapErr(err)
		}
		out[metric] = total
	}
	return out, mapErr(rows.Err())
}

// ListAllUsage returns recent usage records platform-wide, newest first, hard-capped
// at MaxPageLimit so it can never trigger an unbounded full-table scan. Admin
// aggregates go through SumUsageByMetric (SQL-side), not this method.
func (s *PostgresStore) ListAllUsage(ctx context.Context) ([]domain.UsageRecord, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, org_id, metric, quantity, at FROM usage_records ORDER BY at DESC, id DESC LIMIT $1`, MaxPageLimit)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make([]domain.UsageRecord, 0)
	for rows.Next() {
		var u domain.UsageRecord
		if err := rows.Scan(&u.ID, &u.OrgID, &u.Metric, &u.Quantity, &u.At); err != nil {
			return nil, mapErr(err)
		}
		out = append(out, u)
	}
	return out, mapErr(rows.Err())
}
