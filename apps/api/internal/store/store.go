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
)

// Store is the aggregate persistence interface for the control-plane.
type Store interface {
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
	ListBuildsByApp(ctx context.Context, appID string) ([]domain.Build, error)
	UpdateBuild(ctx context.Context, b *domain.Build) error

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

	// App environment variables (per app; key -> value).
	GetAppEnv(ctx context.Context, appID string) (map[string]string, error)
	SetAppEnv(ctx context.Context, appID, key, value string) error
	DeleteAppEnv(ctx context.Context, appID, key string) error

	// App domains.
	CreateDomain(ctx context.Context, d *domain.Domain) error
	GetDomain(ctx context.Context, id string) (*domain.Domain, error)
	ListDomainsByApp(ctx context.Context, appID string) ([]domain.Domain, error)
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
	RevokeAllUserRefreshTokens(ctx context.Context, userID string) error

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
	ListUsageByOrg(ctx context.Context, orgID string) ([]domain.UsageRecord, error)
	// ListUsageByOrgSince returns an org's usage records with At >= since, so the
	// billing summary and invoice math can scope to the current billing period.
	ListUsageByOrgSince(ctx context.Context, orgID string, since time.Time) ([]domain.UsageRecord, error)

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
