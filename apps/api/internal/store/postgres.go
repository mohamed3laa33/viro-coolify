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

// mapErr converts pgx/pg errors into the store's sentinel errors.
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrConflict
	}
	return err
}

// ---- Migrations ----

// Migrate applies all embedded SQL migrations, tracking applied versions in the
// schema_migrations table. It is idempotent.
func (s *PostgresStore) Migrate(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
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
		if err := s.pool.QueryRow(ctx,
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
		if _, err := s.pool.Exec(ctx, string(body)); err != nil {
			return fmt.Errorf("store: apply migration %s: %w", name, err)
		}
		if _, err := s.pool.Exec(ctx,
			`INSERT INTO schema_migrations (version) VALUES ($1)`, name,
		); err != nil {
			return fmt.Errorf("store: record migration %s: %w", name, err)
		}
	}
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
		`INSERT INTO organizations (id, name, slug, created_at) VALUES ($1, $2, $3, $4)`,
		o.ID, o.Name, o.Slug, o.CreatedAt,
	)
	return mapErr(err)
}

func (s *PostgresStore) GetOrganization(ctx context.Context, id string) (*domain.Organization, error) {
	var o domain.Organization
	err := s.pool.QueryRow(ctx,
		`SELECT id, name, slug, created_at FROM organizations WHERE id = $1`, id,
	).Scan(&o.ID, &o.Name, &o.Slug, &o.CreatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &o, nil
}

func (s *PostgresStore) ListOrganizationsForUser(ctx context.Context, userID string) ([]domain.Organization, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT o.id, o.name, o.slug, o.created_at
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
		if err := rows.Scan(&o.ID, &o.Name, &o.Slug, &o.CreatedAt); err != nil {
			return nil, mapErr(err)
		}
		out = append(out, o)
	}
	return out, mapErr(rows.Err())
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

// ---- Apps ----

func (s *PostgresStore) CreateApp(ctx context.Context, a *domain.App) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO apps (id, org_id, project_id, coolify_uuid, name, git_repository, git_branch, build_pack, cpu, memory_mb, status, namespace, "release", host, image, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`,
		a.ID, a.OrgID, a.ProjectID, a.CoolifyUUID, a.Name, a.GitRepository, a.GitBranch, a.BuildPack, a.CPU, a.MemoryMB, a.Status, a.Namespace, a.Release, a.Host, a.Image, a.CreatedAt,
	)
	return mapErr(err)
}

func (s *PostgresStore) GetApp(ctx context.Context, id string) (*domain.App, error) {
	return s.scanApp(s.pool.QueryRow(ctx,
		`SELECT id, org_id, project_id, coolify_uuid, name, git_repository, git_branch, build_pack, cpu, memory_mb, status, namespace, "release", host, image, created_at
		 FROM apps WHERE id = $1`, id))
}

func (s *PostgresStore) scanApp(row pgx.Row) (*domain.App, error) {
	var a domain.App
	if err := row.Scan(&a.ID, &a.OrgID, &a.ProjectID, &a.CoolifyUUID, &a.Name, &a.GitRepository, &a.GitBranch, &a.BuildPack, &a.CPU, &a.MemoryMB, &a.Status, &a.Namespace, &a.Release, &a.Host, &a.Image, &a.CreatedAt); err != nil {
		return nil, mapErr(err)
	}
	return &a, nil
}

func (s *PostgresStore) ListAppsByOrg(ctx context.Context, orgID string) ([]domain.App, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, org_id, project_id, coolify_uuid, name, git_repository, git_branch, build_pack, cpu, memory_mb, status, namespace, "release", host, image, created_at
		 FROM apps WHERE org_id = $1`, orgID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make([]domain.App, 0)
	for rows.Next() {
		var a domain.App
		if err := rows.Scan(&a.ID, &a.OrgID, &a.ProjectID, &a.CoolifyUUID, &a.Name, &a.GitRepository, &a.GitBranch, &a.BuildPack, &a.CPU, &a.MemoryMB, &a.Status, &a.Namespace, &a.Release, &a.Host, &a.Image, &a.CreatedAt); err != nil {
			return nil, mapErr(err)
		}
		out = append(out, a)
	}
	return out, mapErr(rows.Err())
}

func (s *PostgresStore) UpdateApp(ctx context.Context, a *domain.App) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE apps SET org_id = $2, project_id = $3, coolify_uuid = $4, name = $5, git_repository = $6,
		 git_branch = $7, build_pack = $8, cpu = $9, memory_mb = $10, status = $11,
		 namespace = $12, "release" = $13, host = $14, image = $15, created_at = $16
		 WHERE id = $1`,
		a.ID, a.OrgID, a.ProjectID, a.CoolifyUUID, a.Name, a.GitRepository, a.GitBranch, a.BuildPack, a.CPU, a.MemoryMB, a.Status, a.Namespace, a.Release, a.Host, a.Image, a.CreatedAt,
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

// ---- Databases ----

func (s *PostgresStore) CreateDatabase(ctx context.Context, d *domain.Database) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO databases (id, org_id, project_id, coolify_uuid, name, engine, cpu, memory_mb, status, namespace, "release", host, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		d.ID, d.OrgID, d.ProjectID, d.CoolifyUUID, d.Name, d.Engine, d.CPU, d.MemoryMB, d.Status, d.Namespace, d.Release, d.Host, d.CreatedAt,
	)
	return mapErr(err)
}

func (s *PostgresStore) scanDatabase(row pgx.Row) (*domain.Database, error) {
	var d domain.Database
	if err := row.Scan(&d.ID, &d.OrgID, &d.ProjectID, &d.CoolifyUUID, &d.Name, &d.Engine, &d.CPU, &d.MemoryMB, &d.Status, &d.Namespace, &d.Release, &d.Host, &d.CreatedAt); err != nil {
		return nil, mapErr(err)
	}
	return &d, nil
}

func (s *PostgresStore) GetDatabase(ctx context.Context, id string) (*domain.Database, error) {
	return s.scanDatabase(s.pool.QueryRow(ctx,
		`SELECT id, org_id, project_id, coolify_uuid, name, engine, cpu, memory_mb, status, namespace, "release", host, created_at
		 FROM databases WHERE id = $1`, id))
}

func (s *PostgresStore) ListDatabasesByOrg(ctx context.Context, orgID string) ([]domain.Database, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, org_id, project_id, coolify_uuid, name, engine, cpu, memory_mb, status, namespace, "release", host, created_at
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
		`INSERT INTO services (id, org_id, project_id, template, name, coolify_uuid, cpu, memory_mb, status, namespace, "release", host, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		svc.ID, svc.OrgID, svc.ProjectID, svc.Template, svc.Name, svc.CoolifyUUID, svc.CPU, svc.MemoryMB, svc.Status, svc.Namespace, svc.Release, svc.Host, svc.CreatedAt,
	)
	return mapErr(err)
}

func (s *PostgresStore) GetService(ctx context.Context, id string) (*domain.Service, error) {
	var svc domain.Service
	err := s.pool.QueryRow(ctx,
		`SELECT id, org_id, project_id, template, name, coolify_uuid, cpu, memory_mb, status, namespace, "release", host, created_at
		 FROM services WHERE id = $1`, id,
	).Scan(&svc.ID, &svc.OrgID, &svc.ProjectID, &svc.Template, &svc.Name, &svc.CoolifyUUID, &svc.CPU, &svc.MemoryMB, &svc.Status, &svc.Namespace, &svc.Release, &svc.Host, &svc.CreatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &svc, nil
}

func (s *PostgresStore) ListServicesByOrg(ctx context.Context, orgID string) ([]domain.Service, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, org_id, project_id, template, name, coolify_uuid, cpu, memory_mb, status, namespace, "release", host, created_at
		 FROM services WHERE org_id = $1`, orgID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make([]domain.Service, 0)
	for rows.Next() {
		var svc domain.Service
		if err := rows.Scan(&svc.ID, &svc.OrgID, &svc.ProjectID, &svc.Template, &svc.Name, &svc.CoolifyUUID, &svc.CPU, &svc.MemoryMB, &svc.Status, &svc.Namespace, &svc.Release, &svc.Host, &svc.CreatedAt); err != nil {
			return nil, mapErr(err)
		}
		out = append(out, svc)
	}
	return out, mapErr(rows.Err())
}

func (s *PostgresStore) UpdateService(ctx context.Context, svc *domain.Service) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE services SET org_id = $2, project_id = $3, template = $4, name = $5, coolify_uuid = $6,
		 cpu = $7, memory_mb = $8, status = $9, namespace = $10, "release" = $11, host = $12, created_at = $13 WHERE id = $1`,
		svc.ID, svc.OrgID, svc.ProjectID, svc.Template, svc.Name, svc.CoolifyUUID, svc.CPU, svc.MemoryMB, svc.Status, svc.Namespace, svc.Release, svc.Host, svc.CreatedAt,
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

func (s *PostgresStore) SetAppEnv(ctx context.Context, appID, key, value string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO app_env (app_id, key, value) VALUES ($1, $2, $3)
		 ON CONFLICT (app_id, key) DO UPDATE SET value = EXCLUDED.value`,
		appID, key, value,
	)
	return mapErr(err)
}

func (s *PostgresStore) DeleteAppEnv(ctx context.Context, appID, key string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM app_env WHERE app_id = $1 AND key = $2`, appID, key)
	return mapErr(err)
}

// ---- Domains ----

func (s *PostgresStore) CreateDomain(ctx context.Context, d *domain.Domain) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO domains (id, org_id, app_id, domain, verified, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		d.ID, d.OrgID, d.AppID, d.Domain, d.Verified, d.CreatedAt,
	)
	return mapErr(err)
}

func (s *PostgresStore) GetDomain(ctx context.Context, id string) (*domain.Domain, error) {
	var d domain.Domain
	err := s.pool.QueryRow(ctx,
		`SELECT id, org_id, app_id, domain, verified, created_at FROM domains WHERE id = $1`, id,
	).Scan(&d.ID, &d.OrgID, &d.AppID, &d.Domain, &d.Verified, &d.CreatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &d, nil
}

func (s *PostgresStore) ListDomainsByApp(ctx context.Context, appID string) ([]domain.Domain, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, org_id, app_id, domain, verified, created_at FROM domains WHERE app_id = $1`, appID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make([]domain.Domain, 0)
	for rows.Next() {
		var d domain.Domain
		if err := rows.Scan(&d.ID, &d.OrgID, &d.AppID, &d.Domain, &d.Verified, &d.CreatedAt); err != nil {
			return nil, mapErr(err)
		}
		out = append(out, d)
	}
	return out, mapErr(rows.Err())
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
		`INSERT INTO invitations (id, org_id, project_id, email, role, token, status, invited_by, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		inv.ID, inv.OrgID, inv.ProjectID, inv.Email, string(inv.Role), inv.Token, string(inv.Status), inv.InvitedBy, inv.CreatedAt,
	)
	return mapErr(err)
}

func (s *PostgresStore) GetInvitationByToken(ctx context.Context, token string) (*domain.Invitation, error) {
	return s.scanInvitation(s.pool.QueryRow(ctx,
		`SELECT id, org_id, project_id, email, role, token, status, invited_by, created_at
		 FROM invitations WHERE token = $1`, token))
}

func (s *PostgresStore) scanInvitation(row pgx.Row) (*domain.Invitation, error) {
	var inv domain.Invitation
	var role, status string
	if err := row.Scan(&inv.ID, &inv.OrgID, &inv.ProjectID, &inv.Email, &role, &inv.Token, &status, &inv.InvitedBy, &inv.CreatedAt); err != nil {
		return nil, mapErr(err)
	}
	inv.Role = domain.Role(role)
	inv.Status = domain.InvitationStatus(status)
	return &inv, nil
}

func (s *PostgresStore) ListInvitationsByOrg(ctx context.Context, orgID string) ([]domain.Invitation, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, org_id, project_id, email, role, token, status, invited_by, created_at
		 FROM invitations WHERE org_id = $1`, orgID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make([]domain.Invitation, 0)
	for rows.Next() {
		var inv domain.Invitation
		var role, status string
		if err := rows.Scan(&inv.ID, &inv.OrgID, &inv.ProjectID, &inv.Email, &role, &inv.Token, &status, &inv.InvitedBy, &inv.CreatedAt); err != nil {
			return nil, mapErr(err)
		}
		inv.Role = domain.Role(role)
		inv.Status = domain.InvitationStatus(status)
		out = append(out, inv)
	}
	return out, mapErr(rows.Err())
}

func (s *PostgresStore) UpdateInvitation(ctx context.Context, inv *domain.Invitation) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE invitations SET org_id = $2, project_id = $3, email = $4, role = $5, token = $6,
		 status = $7, invited_by = $8, created_at = $9 WHERE id = $1`,
		inv.ID, inv.OrgID, inv.ProjectID, inv.Email, string(inv.Role), inv.Token, string(inv.Status), inv.InvitedBy, inv.CreatedAt,
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
		`INSERT INTO refresh_tokens (id, user_id, revoked, created_at) VALUES ($1, $2, $3, $4)`,
		rt.ID, rt.UserID, rt.Revoked, rt.CreatedAt,
	)
	return mapErr(err)
}

func (s *PostgresStore) GetRefreshToken(ctx context.Context, id string) (*domain.RefreshToken, error) {
	var rt domain.RefreshToken
	err := s.pool.QueryRow(ctx,
		`SELECT id, user_id, revoked, created_at FROM refresh_tokens WHERE id = $1`, id,
	).Scan(&rt.ID, &rt.UserID, &rt.Revoked, &rt.CreatedAt)
	if err != nil {
		return nil, mapErr(err)
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

func (s *PostgresStore) RevokeAllUserRefreshTokens(ctx context.Context, userID string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE refresh_tokens SET revoked = true WHERE user_id = $1 AND revoked = false`, userID)
	return mapErr(err)
}

// ---- Billing: subscriptions & usage ----

func (s *PostgresStore) UpsertSubscription(ctx context.Context, sub *domain.Subscription) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO subscriptions (org_id, plan_id, status, stripe_customer_id, stripe_subscription_id, created_at, current_period_end)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 ON CONFLICT (org_id) DO UPDATE SET
		   plan_id = EXCLUDED.plan_id,
		   status = EXCLUDED.status,
		   stripe_customer_id = EXCLUDED.stripe_customer_id,
		   stripe_subscription_id = EXCLUDED.stripe_subscription_id,
		   created_at = EXCLUDED.created_at,
		   current_period_end = EXCLUDED.current_period_end`,
		sub.OrgID, sub.PlanID, string(sub.Status), sub.StripeCustomerID, sub.StripeSubscriptionID, sub.CreatedAt, sub.CurrentPeriodEnd,
	)
	return mapErr(err)
}

func (s *PostgresStore) GetSubscription(ctx context.Context, orgID string) (*domain.Subscription, error) {
	var sub domain.Subscription
	var status string
	err := s.pool.QueryRow(ctx,
		`SELECT org_id, plan_id, status, stripe_customer_id, stripe_subscription_id, created_at, current_period_end
		 FROM subscriptions WHERE org_id = $1`, orgID,
	).Scan(&sub.OrgID, &sub.PlanID, &status, &sub.StripeCustomerID, &sub.StripeSubscriptionID, &sub.CreatedAt, &sub.CurrentPeriodEnd)
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

func (s *PostgresStore) ListUsageByOrg(ctx context.Context, orgID string) ([]domain.UsageRecord, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, org_id, metric, quantity, at FROM usage_records WHERE org_id = $1`, orgID)
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
		 memory_overcommit_factor, default_region, regions FROM platform_settings WHERE id = true`,
	).Scan(&ps.DefaultCPU, &ps.DefaultMemoryMB, &ps.DefaultPlanID, &ps.CPUOvercommitFactor,
		&ps.MemoryOvercommitFactor, &ps.DefaultRegion, &ps.Regions)
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
		 cpu_overcommit_factor, memory_overcommit_factor, default_region, regions)
		 VALUES (true, $1, $2, $3, $4, $5, $6, $7)
		 ON CONFLICT (id) DO UPDATE SET
		   default_cpu = EXCLUDED.default_cpu,
		   default_memory_mb = EXCLUDED.default_memory_mb,
		   default_plan_id = EXCLUDED.default_plan_id,
		   cpu_overcommit_factor = EXCLUDED.cpu_overcommit_factor,
		   memory_overcommit_factor = EXCLUDED.memory_overcommit_factor,
		   default_region = EXCLUDED.default_region,
		   regions = EXCLUDED.regions`,
		in.DefaultCPU, in.DefaultMemoryMB, in.DefaultPlanID, in.CPUOvercommitFactor,
		in.MemoryOvercommitFactor, in.DefaultRegion, regions,
	)
	return mapErr(err)
}

// ---- Admin overview helpers ----

func (s *PostgresStore) ListAllOrgs(ctx context.Context) ([]domain.Organization, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, name, slug, created_at FROM organizations`)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make([]domain.Organization, 0)
	for rows.Next() {
		var o domain.Organization
		if err := rows.Scan(&o.ID, &o.Name, &o.Slug, &o.CreatedAt); err != nil {
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
		`SELECT org_id, plan_id, status, stripe_customer_id, stripe_subscription_id, created_at, current_period_end
		 FROM subscriptions`)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make([]domain.Subscription, 0)
	for rows.Next() {
		var sub domain.Subscription
		var status string
		if err := rows.Scan(&sub.OrgID, &sub.PlanID, &status, &sub.StripeCustomerID, &sub.StripeSubscriptionID, &sub.CreatedAt, &sub.CurrentPeriodEnd); err != nil {
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

func (s *PostgresStore) ListAllUsage(ctx context.Context) ([]domain.UsageRecord, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, org_id, metric, quantity, at FROM usage_records`)
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
