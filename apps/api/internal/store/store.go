// Package store defines Viro's persistence interfaces and ships an in-memory
// implementation. A Postgres implementation satisfies the same interface.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
)

// Sentinel errors returned by Store implementations.
var (
	ErrNotFound = errors.New("store: not found")
	ErrConflict = errors.New("store: already exists")
	// ErrInvalid signals that a write violated a referential/shape constraint the
	// store enforces (a foreign-key reference to a missing row, a NULL in a
	// NOT-NULL column, or a CHECK violation). The HTTP layer maps it to 400/422
	// rather than a raw 500 — it is a client/data error, not a server fault.
	ErrInvalid = errors.New("store: invalid reference or value")
)

// Pagination defaults for the growth-prone list reads.
const (
	// DefaultPageLimit is applied when a caller requests a bounded page without a
	// positive limit.
	DefaultPageLimit = 50
	// MaxPageLimit caps a single page so a hostile/large ?limit= can never force a
	// huge scan.
	MaxPageLimit = 200
)

// Page bounds a list read. A non-positive Limit on a HTTP-facing read normalises
// to DefaultPageLimit (capped at MaxPageLimit) via Normalize; the store layer
// treats Limit <= 0 as "unbounded" so internal callers that must read the full
// set (e.g. billing summation over a period) can pass the zero value.
type Page struct {
	Limit  int
	Offset int
}

// Normalize returns a Page bounded for a public/list endpoint: a non-positive
// Limit becomes DefaultPageLimit, anything above MaxPageLimit is clamped, and a
// negative Offset becomes 0. Use it in the HTTP layer before handing a Page to
// the store so user input can never request an unbounded or huge scan.
func (p Page) Normalize() Page {
	out := p
	if out.Limit <= 0 {
		out.Limit = DefaultPageLimit
	}
	if out.Limit > MaxPageLimit {
		out.Limit = MaxPageLimit
	}
	if out.Offset < 0 {
		out.Offset = 0
	}
	return out
}

// Bounded reports whether the page applies a LIMIT (Limit > 0). The store uses
// it to decide whether to append LIMIT/OFFSET; an unbounded (zero) page reads
// the full set for internal aggregation callers.
func (p Page) Bounded() bool { return p.Limit > 0 }

// Store is the aggregate persistence interface for the control-plane.
type Store interface {
	// WithTx runs fn inside a single transaction, passing a transaction-scoped
	// Store. On a nil error the transaction commits; on a non-nil error (or a
	// panic) it rolls back. Postgres provides true ACID rollback; the in-memory
	// store runs fn under its write lock for serialization but CANNOT roll back a
	// partial mutation (documented best-effort — callers must treat a returned
	// error as "state may be partially applied" only for the memory store, never
	// for Postgres). The passed Store MUST be used for all writes inside fn.
	WithTx(ctx context.Context, fn func(tx Store) error) error

	// Users.
	CreateUser(ctx context.Context, u *domain.User) error
	GetUserByID(ctx context.Context, id string) (*domain.User, error)
	GetUserByEmail(ctx context.Context, email string) (*domain.User, error)
	UpdateUser(ctx context.Context, u *domain.User) error

	// Organizations.
	CreateOrganization(ctx context.Context, o *domain.Organization) error
	GetOrganization(ctx context.Context, id string) (*domain.Organization, error)
	ListOrganizationsForUser(ctx context.Context, userID string) ([]domain.Organization, error)
	// UpdateOrg persists the mutable org fields (name, billing email).
	UpdateOrg(ctx context.Context, o *domain.Organization) error

	// Memberships.
	AddMembership(ctx context.Context, m domain.Membership) error
	GetMembership(ctx context.Context, orgID, userID string) (*domain.Membership, error)
	ListMemberships(ctx context.Context, orgID string) ([]domain.Membership, error)
	// UpdateMembershipRole changes an existing member's role within an org.
	UpdateMembershipRole(ctx context.Context, orgID, userID string, role domain.Role) error
	// RemoveMembership removes a member from an org.
	RemoveMembership(ctx context.Context, orgID, userID string) error

	// Apps (tenant-scoped).
	CreateApp(ctx context.Context, a *domain.App) error
	GetApp(ctx context.Context, id string) (*domain.App, error)
	ListAppsByOrg(ctx context.Context, orgID string) ([]domain.App, error)
	UpdateApp(ctx context.Context, a *domain.App) error
	DeleteApp(ctx context.Context, id string) error

	// Builds (git-source image builds, tenant-scoped via the owning app/org).
	CreateBuild(ctx context.Context, b *domain.Build) error
	GetBuild(ctx context.Context, id string) (*domain.Build, error)
	// ListBuildsByApp returns the app's builds newest-first. A bounded Page applies
	// LIMIT/OFFSET; the zero Page reads all builds.
	ListBuildsByApp(ctx context.Context, appID string, p Page) ([]domain.Build, error)
	UpdateBuild(ctx context.Context, b *domain.Build) error

	// Releases (immutable per-deploy revisions of an app, tenant-scoped via the
	// owning app/org). CreateRelease records one revision; GetRelease fetches by id;
	// ListReleasesByApp returns the app's revisions newest-first (revision desc);
	// UpdateRelease persists a status/note change (e.g. superseded -> active).
	// A bounded Page applies LIMIT/OFFSET; the zero Page reads all revisions (the
	// revision-allocation/rollback paths need the full history).
	CreateRelease(ctx context.Context, rel *domain.Release) error
	GetRelease(ctx context.Context, id string) (*domain.Release, error)
	ListReleasesByApp(ctx context.Context, appID string, p Page) ([]domain.Release, error)
	UpdateRelease(ctx context.Context, rel *domain.Release) error

	// Databases (tenant-scoped).
	CreateDatabase(ctx context.Context, d *domain.Database) error
	GetDatabase(ctx context.Context, id string) (*domain.Database, error)
	ListDatabasesByOrg(ctx context.Context, orgID string) ([]domain.Database, error)
	UpdateDatabase(ctx context.Context, d *domain.Database) error
	DeleteDatabase(ctx context.Context, id string) error

	// Services (one-click catalog instances, tenant-scoped).
	CreateService(ctx context.Context, svc *domain.Service) error
	GetService(ctx context.Context, id string) (*domain.Service, error)
	ListServicesByOrg(ctx context.Context, orgID string) ([]domain.Service, error)
	UpdateService(ctx context.Context, svc *domain.Service) error
	DeleteService(ctx context.Context, id string) error

	// App environment variables (per app; key -> value). GetAppEnv returns the
	// stored values (secret entries are the AT-REST/encrypted value; the deploy
	// path decrypts). ListAppEnv returns each entry with its secret flag so the
	// API can mask secret values. SetAppEnv records whether the entry is a secret
	// (the value passed in is already encrypted-at-rest for secrets).
	GetAppEnv(ctx context.Context, appID string) (map[string]string, error)
	ListAppEnv(ctx context.Context, appID string) ([]domain.AppEnvEntry, error)
	SetAppEnv(ctx context.Context, appID, key, value string, secret bool) error
	DeleteAppEnv(ctx context.Context, appID, key string) error

	// Audit log (append-only). CreateAuditEvent appends one event; ListAuditEvents
	// returns the most-recent-first events matching the filter (org-scoped, or
	// platform-level when OrgID is empty).
	CreateAuditEvent(ctx context.Context, e *domain.AuditEvent) error
	ListAuditEvents(ctx context.Context, f domain.AuditFilter) ([]domain.AuditEvent, error)
	// CountAuditEvents returns the total number of audit events matching the filter
	// (ignoring Limit/Offset), so the list endpoint can report has-more/total.
	CountAuditEvents(ctx context.Context, f domain.AuditFilter) (int, error)

	// App domains.
	CreateDomain(ctx context.Context, d *domain.Domain) error
	GetDomain(ctx context.Context, id string) (*domain.Domain, error)
	ListDomainsByApp(ctx context.Context, appID string) ([]domain.Domain, error)
	// GetVerifiedDomainByHost returns the single VERIFIED domain row owning the
	// given host (case-insensitive match on the domain column WHERE
	// status='verified'), regardless of app/org. It is the global hostname-ownership
	// lookup used to reject a second tenant verifying a host already claimed by
	// another. Returns ErrNotFound when no verified row owns the host.
	GetVerifiedDomainByHost(ctx context.Context, host string) (*domain.Domain, error)
	UpdateDomain(ctx context.Context, d *domain.Domain) error
	DeleteDomain(ctx context.Context, id string) error

	// Projects (Org → Project → App).
	CreateProject(ctx context.Context, p *domain.Project) error
	GetProject(ctx context.Context, id string) (*domain.Project, error)
	ListProjectsByOrg(ctx context.Context, orgID string) ([]domain.Project, error)
	// DeleteProject removes an empty project. It returns ErrConflict if the
	// project still owns any apps or services, and ErrNotFound if it does not
	// exist within the given org.
	DeleteProject(ctx context.Context, orgID, projectID string) error

	// Project memberships.
	AddProjectMembership(ctx context.Context, m domain.ProjectMembership) error
	GetProjectMembership(ctx context.Context, projectID, userID string) (*domain.ProjectMembership, error)

	// Invitations.
	CreateInvitation(ctx context.Context, inv *domain.Invitation) error
	GetInvitationByToken(ctx context.Context, token string) (*domain.Invitation, error)
	ListInvitationsByOrg(ctx context.Context, orgID string) ([]domain.Invitation, error)
	UpdateInvitation(ctx context.Context, inv *domain.Invitation) error
	// RevokeInvitation marks an org's invitation as revoked. It returns
	// ErrNotFound when no matching invitation exists within the org.
	RevokeInvitation(ctx context.Context, orgID, inviteID string) error

	// Refresh tokens (rotation + revocation; keyed by jti).
	CreateRefreshToken(ctx context.Context, rt *domain.RefreshToken) error
	GetRefreshToken(ctx context.Context, id string) (*domain.RefreshToken, error)
	RevokeRefreshToken(ctx context.Context, id string) error
	// RevokeRefreshTokenIfActive atomically revokes the token only when it is
	// currently active (not yet revoked). It reports revoked=true when this call
	// performed the revocation and revoked=false when the row was already revoked
	// (a lost rotation race or a replay). A missing row returns ErrNotFound. This
	// is the compare-and-set primitive that makes refresh rotation race-free.
	RevokeRefreshTokenIfActive(ctx context.Context, id string) (revoked bool, err error)
	RevokeAllUserRefreshTokens(ctx context.Context, userID string) error
	// DeleteExpiredRefreshTokens deletes ONLY truly-expired refresh-token rows
	// (ExpiresAt < before), returning the number removed. It is the GC the cleanup
	// ticker runs so the table does not grow without bound. It deliberately KEEPS
	// revoked-but-unexpired rows: a revoked row is the replay-detection tombstone a
	// rotated token needs, so a late replay still triggers family revocation. The
	// tombstone survives until its underlying JWT can no longer Verify.
	DeleteExpiredRefreshTokens(ctx context.Context, before time.Time) (int64, error)

	// Password reset tokens (single-use, time-limited, hashed at rest).
	CreatePasswordResetToken(ctx context.Context, t *domain.PasswordResetToken) error
	// InvalidateUserPasswordResetTokens marks ALL of a user's currently-unused reset
	// tokens as used (used_at = now), so issuing a fresh reset token first revokes any
	// prior outstanding ones — only the most recent reset link is ever live.
	InvalidateUserPasswordResetTokens(ctx context.Context, userID string, usedAt time.Time) error
	// GetPasswordResetTokenByHash resolves a reset token by its SHA-256 hash.
	// Returns ErrNotFound when no row matches.
	GetPasswordResetTokenByHash(ctx context.Context, tokenHash string) (*domain.PasswordResetToken, error)
	// ConsumePasswordResetToken atomically marks the token used at usedAt only when
	// it is currently unused. It reports consumed=false (no error) when the row was
	// already used (replay), so the reset flow can reject reuse. ErrNotFound when
	// the row does not exist.
	ConsumePasswordResetToken(ctx context.Context, id string, usedAt time.Time) (consumed bool, err error)

	// API tokens (personal access tokens; "vrt_<random>"). Only the SHA-256 hash
	// of the full token is stored — never the plaintext. CreateApiToken persists a
	// new token; GetApiTokenByHash resolves a token by its hash for Bearer auth (it
	// returns the row regardless of expiry — the caller enforces expiry so it can
	// distinguish "unknown" from "expired"); ListApiTokensByUser returns a user's
	// tokens newest-first (never the secret); DeleteApiToken revokes a token scoped
	// to its owner (ErrNotFound when no matching token exists for the user);
	// TouchApiToken records the last-used time best-effort.
	CreateApiToken(ctx context.Context, t *domain.ApiToken) error
	GetApiTokenByHash(ctx context.Context, tokenHash string) (*domain.ApiToken, error)
	ListApiTokensByUser(ctx context.Context, userID string) ([]domain.ApiToken, error)
	DeleteApiToken(ctx context.Context, userID, id string) error
	TouchApiToken(ctx context.Context, id string, lastUsedAt time.Time) error

	// Billing.
	UpsertSubscription(ctx context.Context, s *domain.Subscription) error
	GetSubscription(ctx context.Context, orgID string) (*domain.Subscription, error)
	// GetSubscriptionByStripeID resolves an org's subscription by its stored Stripe
	// subscription id (sub_…) so a webhook can map an event back to an org without
	// metadata. Returns ErrNotFound when no subscription carries that id.
	GetSubscriptionByStripeID(ctx context.Context, stripeSubID string) (*domain.Subscription, error)
	// GetSubscriptionByCustomerID resolves an org's subscription by its stored
	// Stripe customer id (cus_…). Returns ErrNotFound when none matches.
	GetSubscriptionByCustomerID(ctx context.Context, customerID string) (*domain.Subscription, error)
	AddUsage(ctx context.Context, u *domain.UsageRecord) error
	// AddUsageIfAbsent inserts a usage record keyed by its deterministic id, ATOMICALLY:
	// it reports inserted=true on the first write and inserted=false when a record with
	// that id already exists (no error, no duplicate). This is the race-free primitive
	// the metering loop uses for per-(org,hour) idempotency — postgres does INSERT ...
	// ON CONFLICT (id) DO NOTHING + RowsAffected; memory dedupes by id under its lock.
	AddUsageIfAbsent(ctx context.Context, u *domain.UsageRecord) (inserted bool, err error)
	// ListUsageByOrg returns an org's usage records newest-first. A bounded Page
	// applies LIMIT/OFFSET; the zero Page reads all records.
	ListUsageByOrg(ctx context.Context, orgID string, p Page) ([]domain.UsageRecord, error)
	// ListUsageByOrgSince returns an org's usage records with At >= since, newest
	// first, so the billing summary and invoice math can scope to the current
	// billing period. A bounded Page applies LIMIT/OFFSET; the zero Page reads all
	// matching records (billing summation needs the full period).
	ListUsageByOrgSince(ctx context.Context, orgID string, since time.Time, p Page) ([]domain.UsageRecord, error)

	// Stripe webhook idempotency. EventProcessed is a read-only peek reporting
	// whether the event id has already been recorded, so the webhook can fast-path a
	// redelivery WITHOUT marking it (the mark must happen only after a successful
	// apply). MarkEventProcessed records a Stripe event id and reports whether it was
	// newly inserted (true) or already present (false). Both are safe to call
	// concurrently; the postgres mark uses ON CONFLICT DO NOTHING + RowsAffected.
	EventProcessed(ctx context.Context, eventID string) (seen bool, err error)
	MarkEventProcessed(ctx context.Context, eventID string) (firstTime bool, err error)

	// Metering progress (singleton). GetMeterState returns ErrNotFound before the
	// first run so the caller can seed the catch-up window.
	GetMeterState(ctx context.Context) (*domain.MeterState, error)
	SetMeterState(ctx context.Context, st *domain.MeterState) error

	// Plans (billing catalog, super-admin managed).
	ListPlans(ctx context.Context) ([]domain.Plan, error)
	GetPlan(ctx context.Context, id string) (*domain.Plan, error)
	UpsertPlan(ctx context.Context, p *domain.Plan) error
	DeletePlan(ctx context.Context, id string) error

	// Pricing components (hourly resource prices, super-admin managed).
	ListPricingComponents(ctx context.Context) ([]domain.PricingComponent, error)
	GetPricingComponent(ctx context.Context, key string) (*domain.PricingComponent, error)
	UpsertPricingComponent(ctx context.Context, p *domain.PricingComponent) error
	DeletePricingComponent(ctx context.Context, key string) error

	// Service templates (one-click catalog, super-admin managed).
	ListServiceTemplates(ctx context.Context) ([]domain.ServiceTemplate, error)
	GetServiceTemplate(ctx context.Context, key string) (*domain.ServiceTemplate, error)
	UpsertServiceTemplate(ctx context.Context, t *domain.ServiceTemplate) error
	DeleteServiceTemplate(ctx context.Context, key string) error

	// Platform settings (singleton, super-admin managed).
	GetSettings(ctx context.Context) (*domain.PlatformSettings, error)
	UpdateSettings(ctx context.Context, s *domain.PlatformSettings) error

	// Admin overview helpers.
	ListAllOrgs(ctx context.Context) ([]domain.Organization, error)
	CountUsers(ctx context.Context) (int, error)
	ListAllSubscriptions(ctx context.Context) ([]domain.Subscription, error)
	ListAllUsage(ctx context.Context) ([]domain.UsageRecord, error)
	// SumUsageByMetric aggregates total usage per metric in the store (SQL-side
	// for Postgres) so the admin overview never scans-and-sums in Go.
	SumUsageByMetric(ctx context.Context) (map[string]int64, error)

	// Close releases any resources held by the store (e.g. a pgx connection
	// pool). The in-memory store is a no-op. Safe to call once on shutdown.
	Close()
}
