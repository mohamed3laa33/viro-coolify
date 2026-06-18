// Package domain holds Viro's core control-plane entities, free of transport or storage concerns.
package domain

import "time"

// Role is a member's role within an organization. Roles are rank-ordered:
// member < admin < owner.
type Role string

const (
	RoleMember Role = "member"
	RoleAdmin  Role = "admin"
	RoleOwner  Role = "owner"
)

// Rank returns the comparable rank of a role (0 for unknown).
func (r Role) Rank() int {
	switch r {
	case RoleMember:
		return 1
	case RoleAdmin:
		return 2
	case RoleOwner:
		return 3
	default:
		return 0
	}
}

// Valid reports whether the role is one of the known roles.
func (r Role) Valid() bool { return r.Rank() > 0 }

// AtLeast reports whether r has at least the privilege of other.
func (r Role) AtLeast(other Role) bool { return r.Rank() >= other.Rank() }

// User is an authenticated principal.
type User struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	Name         string    `json:"name"`
	PasswordHash string    `json:"-"`
	IsAdmin      bool      `json:"isAdmin"` // Viro super-admin (platform-wide)
	CreatedAt    time.Time `json:"createdAt"`
}

// Organization is the tenancy boundary that owns apps, databases and billing.
type Organization struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Slug         string `json:"slug"`
	BillingEmail string `json:"billingEmail,omitempty"`
	// SpendCapCents is a per-org hard ceiling on the current-period charge
	// (ChargeCents = base + size-aware overage). The size-aware metered usage is
	// already folded into overage, so it is NOT added again (no double-count). 0
	// means "no per-org cap" — the platform default cap
	// (PlatformSettings.DefaultSpendCapCents) applies instead. Admin/DB-driven;
	// never hardcoded.
	SpendCapCents int64     `json:"spendCapCents"`
	CreatedAt     time.Time `json:"createdAt"`
}

// Membership links a user to an organization with a role.
type Membership struct {
	OrgID  string `json:"orgId"`
	UserID string `json:"userId"`
	Role   Role   `json:"role"`
}

// Project groups apps and databases within an organization (Org → Project → App).
// Every org has at least a "default" project.
type Project struct {
	ID        string    `json:"id"`
	OrgID     string    `json:"orgId"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	IsDefault bool      `json:"isDefault"`
	CreatedAt time.Time `json:"createdAt"`
}

// ProjectMembership grants a user scoped access to a single project, for
// finer-grained access than full organization membership.
type ProjectMembership struct {
	ProjectID string `json:"projectId"`
	UserID    string `json:"userId"`
	Role      Role   `json:"role"`
}

// InvitationStatus is the lifecycle state of an invitation.
type InvitationStatus string

const (
	InvitePending  InvitationStatus = "pending"
	InviteAccepted InvitationStatus = "accepted"
	InviteRevoked  InvitationStatus = "revoked"
)

// Invitation invites a person (by email) to an organization, or to a specific
// project within it (when ProjectID is set), with a role.
type Invitation struct {
	ID        string           `json:"id"`
	OrgID     string           `json:"orgId"`
	ProjectID string           `json:"projectId,omitempty"` // empty => org-level invite
	Email     string           `json:"email"`
	Role      Role             `json:"role"`
	Token     string           `json:"token"`
	Status    InvitationStatus `json:"status"`
	InvitedBy string           `json:"invitedBy"`
	CreatedAt time.Time        `json:"createdAt"`
	// ExpiresAt is the moment the invitation can no longer be accepted. A zero
	// value means "never expires" (back-compat for rows created before expiry
	// was introduced).
	ExpiresAt time.Time `json:"expiresAt,omitempty"`
}

// App is a Viro application owned by an organization and grouped under a project.
// It is the tenant-scoped record Viro authorizes against and deploys onto the
// Kubernetes backend.
type App struct {
	ID            string  `json:"id"`
	OrgID         string  `json:"orgId"`
	ProjectID     string  `json:"projectId"`
	Name          string  `json:"name"`
	Image         string  `json:"image,omitempty"` // container image; when set the app deploys directly (no build)
	GitRepository string  `json:"gitRepository,omitempty"`
	GitBranch     string  `json:"gitBranch,omitempty"`
	BuildPack     string  `json:"buildPack,omitempty"`
	CPU           float64 `json:"cpu"`      // requested vCPU
	MemoryMB      int     `json:"memoryMb"` // requested memory in MB
	Status        string  `json:"status"`
	// MinReplicas/MaxReplicas are the per-app autoscaling bounds threaded into the
	// KEDA ScaledObject. Zero means "use the platform default" (admin/DB-driven);
	// a STATELESS app may set MinReplicas to 0 to scale to zero. They are set via
	// the scale endpoint and persisted so a redeploy keeps the same bounds.
	MinReplicas int `json:"minReplicas,omitempty"`
	MaxReplicas int `json:"maxReplicas,omitempty"`
	// Kubernetes placement returned by the deploy backend (kube.Backend).
	Namespace string    `json:"namespace,omitempty"` // per-org-project namespace
	Release   string    `json:"release,omitempty"`   // Helm release name
	Host      string    `json:"host,omitempty"`      // generated public hostname
	CreatedAt time.Time `json:"createdAt"`
}

// ReleaseStatus is the lifecycle state of a single app release (revision).
type ReleaseStatus string

const (
	// ReleaseDeploying is a release that has been Applied but not yet observed
	// Running by the reconciler.
	ReleaseDeploying ReleaseStatus = "deploying"
	// ReleaseActive is the release currently serving the app (observed Running, or
	// Applied successfully). At most one release per app is active at a time.
	ReleaseActive ReleaseStatus = "active"
	// ReleaseFailed is a release whose deploy did not come up.
	ReleaseFailed ReleaseStatus = "failed"
	// ReleaseSuperseded is a previously-active release replaced by a newer one.
	ReleaseSuperseded ReleaseStatus = "superseded"
	// ReleaseRolledBack marks a release that has been rolled back away from (it was
	// active and a rollback to an EARLIER revision superseded it). The new release
	// created BY a rollback is itself active and carries Note="rollback to rN".
	ReleaseRolledBack ReleaseStatus = "rolled_back"
)

// Release is one immutable deploy revision of an app: the exact image + resources
// that were Applied, a monotonic per-app Revision, and a ConfigHash fingerprint of
// the full rendered spec (image+env+resources+domains). Every successful Apply for
// an app records a Release; a rollback re-renders a stored Release's snapshot and
// records a NEW Release. Releases give the app a per-deploy history and a target to
// roll back to.
type Release struct {
	ID       string `json:"id"`
	AppID    string `json:"appId"`
	OrgID    string `json:"orgId"`
	Revision int    `json:"revision"` // monotonic per app, starting at 1
	Image    string `json:"image"`
	GitRef   string `json:"gitRef,omitempty"`
	// ConfigHash is a stable fingerprint of the rendered spec (image + env +
	// resources + domains) at deploy time, so two identical deploys hash equal and a
	// change is detectable.
	ConfigHash string `json:"configHash,omitempty"`
	// CPU/MemoryMB capture the resource size this release was deployed with, so a
	// rollback can restore the release-time size (not just the image).
	CPU       float64       `json:"cpu"`
	MemoryMB  int           `json:"memoryMb"`
	Status    ReleaseStatus `json:"status"`
	Note      string        `json:"note,omitempty"` // e.g. "rollback to r2"
	CreatedAt time.Time     `json:"createdAt"`
}

// Service is a one-click catalog instance (WordPress, a database, etc.) owned by
// an organization and grouped under a project, deployed as a Helm release onto
// the Kubernetes backend.
type Service struct {
	ID        string  `json:"id"`
	OrgID     string  `json:"orgId"`
	ProjectID string  `json:"projectId"`
	Template  string  `json:"template"` // catalog template key
	Name      string  `json:"name"`
	CPU       float64 `json:"cpu"`
	MemoryMB  int     `json:"memoryMb"`
	Status    string  `json:"status"`
	// Kubernetes placement returned by the deploy backend (kube.Backend).
	Namespace string    `json:"namespace,omitempty"` // per-org-project namespace
	Release   string    `json:"release,omitempty"`   // Helm release name
	Host      string    `json:"host,omitempty"`      // generated public hostname
	CreatedAt time.Time `json:"createdAt"`
}

// BuildStatus is the lifecycle state of an image build.
type BuildStatus string

const (
	BuildPending   BuildStatus = "pending"
	BuildBuilding  BuildStatus = "building"
	BuildSucceeded BuildStatus = "succeeded"
	BuildFailed    BuildStatus = "failed"
)

// Build records one git-source image build for an app: the source ref it built,
// the image it produced (on success), captured logs (on failure), and timing.
// Builds have no Helm release of their own — once a build succeeds the platform
// deploys the produced image through the normal app deploy path.
type Build struct {
	ID         string      `json:"id"`
	AppID      string      `json:"appId"`
	OrgID      string      `json:"orgId"`
	Status     BuildStatus `json:"status"`
	CommitRef  string      `json:"commitRef,omitempty"` // git ref/commit the build targeted
	Image      string      `json:"image,omitempty"`     // image produced on success
	Logs       string      `json:"logs,omitempty"`      // captured build logs (on failure)
	CreatedAt  time.Time   `json:"createdAt"`
	FinishedAt time.Time   `json:"finishedAt,omitempty"`
}

// DomainStatus is the ownership-verification lifecycle of a custom domain.
type DomainStatus string

const (
	// DomainPending is a freshly-added domain awaiting DNS TXT verification. It is
	// NOT routed and serves no TLS until it becomes verified.
	DomainPending DomainStatus = "pending"
	// DomainVerified means the tenant proved ownership (DNS TXT challenge matched).
	// Only verified domains are attached to the Gateway, issued a TLS certificate,
	// and routed by the app's HTTPRoute.
	DomainVerified DomainStatus = "verified"
	// DomainFailed means the last verification attempt did not find a matching TXT
	// record. The domain may be re-verified once DNS is corrected.
	DomainFailed DomainStatus = "failed"
)

// Domain is a custom domain (FQDN) attached to an app. Ownership is proven via a
// DNS TXT challenge (Status/VerificationToken/VerifiedAt) before the domain is
// ever routed or issued TLS — an unverified domain never serves the tenant.
type Domain struct {
	ID     string `json:"id"`
	OrgID  string `json:"orgId"`
	AppID  string `json:"appId"`
	Domain string `json:"domain"`
	// Verified is kept as a convenience mirror of Status==DomainVerified for
	// back-compat with existing consumers; Status is the source of truth.
	Verified bool         `json:"verified"`
	Status   DomainStatus `json:"status"`
	// VerificationToken is the random value the tenant must publish as a TXT record
	// at _vortex-challenge.<domain> to prove ownership. Generated with crypto/rand.
	VerificationToken string    `json:"verificationToken,omitempty"`
	VerifiedAt        time.Time `json:"verifiedAt,omitempty"`
	CreatedAt         time.Time `json:"createdAt"`
}

// IsVerified reports whether the domain has proven ownership and may be routed.
func (d Domain) IsVerified() bool {
	return d.Status == DomainVerified || (d.Status == "" && d.Verified)
}

// Database is a Vortex managed database owned by an organization. It is deployed
// as a StatefulSet (one-click engine image) into the org's tenant namespace.
type Database struct {
	ID        string  `json:"id"`
	OrgID     string  `json:"orgId"`
	ProjectID string  `json:"projectId,omitempty"`
	Name      string  `json:"name"`
	Engine    string  `json:"engine"`
	CPU       float64 `json:"cpu"`      // requested vCPU
	MemoryMB  int     `json:"memoryMb"` // requested memory in MB
	StorageGB int     `json:"storageGb"`
	Status    string  `json:"status"`
	// Generated connection credentials. The engine container initializes itself
	// with these on first boot. These are NEVER serialized on the bare model
	// (json:"-") so a bulk listing can't leak every database's plaintext
	// password; credentials are exposed ONLY through the connection-info detail
	// endpoint, whose DatabaseConnInfo DTO carries them via explicit fields.
	//
	// TODO(security): these are stored plaintext-at-rest for now. A later security
	// wave must encrypt them (or move the source of truth to a K8s Secret and have
	// the chart mount it via envFrom). Do not block durability/usability on it.
	Username     string `json:"-"`
	Password     string `json:"-"`
	DatabaseName string `json:"-"`
	// Kubernetes placement returned by the deploy backend (kube.Backend).
	Namespace string    `json:"namespace,omitempty"` // per-org-project namespace
	Release   string    `json:"release,omitempty"`   // Helm release name
	Host      string    `json:"host,omitempty"`      // in-cluster service host (internal)
	CreatedAt time.Time `json:"createdAt"`
}

// Plan is a billing plan in the Viro catalog (fly.io-style usage-based pricing).
// Plans are stored in the control-plane store and managed via the super-admin API.
type Plan struct {
	ID                  string  `json:"id"`
	Name                string  `json:"name"`
	Description         string  `json:"description"`
	PriceCents          int     `json:"priceCents"`    // monthly base price
	Currency            string  `json:"currency"`      // e.g. "usd"
	IncludedHours       int     `json:"includedHours"` // included compute-hours per month
	OveragePerHourCents int     `json:"overagePerHourCents"`
	StripePriceID       string  `json:"stripePriceId"` // mapped to a Stripe price when billing is live
	MaxCPU              float64 `json:"maxCpu"`        // max vCPU per workload
	MaxMemoryMB         int     `json:"maxMemoryMb"`   // max memory (MB) per workload
	MaxApps             int     `json:"maxApps"`       // max workloads per org
	IsDefault           bool    `json:"isDefault"`     // the fallback plan for unknown/empty plans
	SortOrder           int     `json:"sortOrder"`     // display order in the catalog
	Active              bool    `json:"active"`        // shown in the public catalog when true
}

// ServiceTemplate is a one-click catalog entry (service, database or app) stored
// in the control-plane store and managed via the super-admin API.
type ServiceTemplate struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Kind        string `json:"kind"` // "service" | "database" | "app"
	Image       string `json:"image,omitempty"`
	DefaultPort int    `json:"defaultPort,omitempty"`
	Active      bool   `json:"active"`
	SortOrder   int    `json:"sortOrder"`
}

// PricingComponent is an admin-managed billable resource priced per hour. The
// platform meters every running workload against these components to compute
// cost. Like all business values, prices live in the store and are edited via
// the super-admin API — never hardcoded.
//
// Canonical components: "cpu" (priced per vCPU-hour) and "memory" (per GB-hour);
// admins may add more (e.g. "storage"). The cost of a workload of size
// (cpu vCPU, mem GB) per hour is cpu*price[cpu] + mem*price[memory] + …
type PricingComponent struct {
	Key          string  `json:"key"` // cpu | memory | storage | <custom>
	Name         string  `json:"name"`
	Unit         string  `json:"unit"`         // e.g. "vCPU-hour", "GB-hour"
	PricePerHour float64 `json:"pricePerHour"` // price (in Currency) per unit, per hour
	Currency     string  `json:"currency"`     // e.g. "usd"
	Active       bool    `json:"active"`
	SortOrder    int     `json:"sortOrder"`
}

// PlatformSettings holds platform-wide defaults managed via the super-admin API.
// It is a singleton.
type PlatformSettings struct {
	DefaultCPU             float64  `json:"defaultCpu"`
	DefaultMemoryMB        int      `json:"defaultMemoryMb"`
	DefaultPlanID          string   `json:"defaultPlanId"`
	CPUOvercommitFactor    float64  `json:"cpuOvercommitFactor"`
	MemoryOvercommitFactor float64  `json:"memoryOvercommitFactor"`
	DefaultRegion          string   `json:"defaultRegion"`
	Regions                []string `json:"regions"`
	// Billing gating policy (admin/DB-driven; never hardcoded).
	//
	// GracePastDue, when true, still allows an org whose subscription is past_due to
	// provision/deploy (a soft grace window); canceled/unpaid are always blocked.
	GracePastDue bool `json:"gracePastDue"`
	// DefaultSpendCapCents is the platform-wide current-period spend ceiling applied
	// to an org that has no per-org SpendCapCents set. 0 disables the default cap.
	DefaultSpendCapCents int64 `json:"defaultSpendCapCents"`

	// KEDA autoscaling defaults (admin/DB-driven; never hardcoded). These drive the
	// ScaledObject the deploy backend renders for every stateless workload, with
	// per-app overrides via App.MinReplicas/MaxReplicas.
	//
	// KedaDefaultMinReplicas is the floor for a stateless app when it sets no
	// per-app override. 0 enables scale-to-zero (the core cost lever). DATABASES
	// always keep a floor of 1 regardless of this value.
	KedaDefaultMinReplicas int `json:"kedaDefaultMinReplicas"`
	// KedaDefaultMaxReplicas is the autoscaling ceiling for a stateless app with no
	// per-app override.
	KedaDefaultMaxReplicas int `json:"kedaDefaultMaxReplicas"`
	// KedaMaxReplicasCeiling is the hard upper bound on a per-app MaxReplicas override
	// (set via the scale endpoint). It stops a tenant from requesting an unbounded
	// MaxReplicas and bypassing the plan/platform autoscaling limit. 0 disables the
	// ceiling. Admin-tunable via the settings PATCH.
	KedaMaxReplicasCeiling int `json:"kedaMaxReplicasCeiling"`
	// KedaPollingInterval is how often (seconds) KEDA evaluates the triggers.
	KedaPollingInterval int `json:"kedaPollingInterval"`
	// KedaCooldownPeriod is how long (seconds) KEDA waits after the last trigger
	// activity before scaling back down (to the idle/min count). A non-zero cooldown
	// is what makes a CPU-trigger scale-to-zero behave sanely without the HTTP add-on.
	KedaCooldownPeriod int `json:"kedaCooldownPeriod"`
	// KedaCPUUtilization is the CPU-utilization target (%) for the default CPU
	// trigger. The CPU trigger is the safe default because it needs no extra add-on.
	KedaCPUUtilization int `json:"kedaCpuUtilization"`
	// KedaHTTPTrigger gates an HTTP-concurrency trigger (true HTTP-wake / scale on
	// request rate). It requires the keda-http-add-on to be installed in the
	// cluster, so it is OFF by default; turning it on without the add-on installed
	// would leave the ScaledObject's HTTP trigger inert. When false the workload
	// uses the CPU trigger only.
	KedaHTTPTrigger bool `json:"kedaHttpTrigger"`
}

// SubscriptionStatus mirrors the lifecycle of a subscription.
type SubscriptionStatus string

const (
	SubActive     SubscriptionStatus = "active"
	SubTrialing   SubscriptionStatus = "trialing"
	SubIncomplete SubscriptionStatus = "incomplete"
	SubPastDue    SubscriptionStatus = "past_due"
	SubUnpaid     SubscriptionStatus = "unpaid"
	SubCanceled   SubscriptionStatus = "canceled"
)

// SubscriptionStatusFromStripe maps a Stripe subscription `status` value to a
// domain status, defaulting to SubIncomplete for unknown values so an unmapped
// state never silently reads as active.
func SubscriptionStatusFromStripe(s string) SubscriptionStatus {
	switch s {
	case "active":
		return SubActive
	case "trialing":
		return SubTrialing
	case "past_due":
		return SubPastDue
	case "unpaid":
		return SubUnpaid
	case "canceled":
		return SubCanceled
	case "incomplete", "incomplete_expired":
		return SubIncomplete
	default:
		return SubIncomplete
	}
}

// Subscription is an organization's billing subscription.
type Subscription struct {
	OrgID                string             `json:"orgId"`
	PlanID               string             `json:"planId"`
	Status               SubscriptionStatus `json:"status"`
	StripeCustomerID     string             `json:"-"`
	StripeSubscriptionID string             `json:"-"`
	// StripeSubscriptionItemID is the metered subscription-ITEM id (si_…) captured
	// from the subscription's items on a customer.subscription.* webhook. Stripe's
	// usage_records endpoint is per-ITEM, so metered usage MUST be reported against
	// this si_ id (a sub_ id 404s). Empty until a subscription event arrives.
	StripeSubscriptionItemID string    `json:"-"`
	CreatedAt                time.Time `json:"createdAt"`
	CurrentPeriodEnd         time.Time `json:"currentPeriodEnd"`
}

// UsageRecord is a metered usage event for an organization.
type UsageRecord struct {
	ID       string    `json:"id"`
	OrgID    string    `json:"orgId"`
	Metric   string    `json:"metric"` // e.g. "compute_hours", "builds", "egress_gb"
	Quantity int64     `json:"quantity"`
	At       time.Time `json:"at"`
}

// MeterState persists the metering loop's progress: the last whole UTC hour that
// has already been metered. It is a singleton row. Catch-up meters every missed
// hour strictly after LastMeteredHour up to (and including) the current hour, so
// a restart or a downtime gap is filled exactly once and never double-counted.
type MeterState struct {
	LastMeteredHour time.Time `json:"lastMeteredHour"`
}

// AuditEvent is an append-only record of a privileged mutation or security-
// relevant event (admin config change, secret write, role/membership change,
// invitation lifecycle, subscription change, auth login/logout/failure). It is
// written by the audit helper on the Server and never carries secret VALUES —
// only the affected key/identifier in TargetID/Metadata.
type AuditEvent struct {
	ID string `json:"id"`
	// OrgID scopes the event to an organization, or is empty ("") for
	// platform-level (super-admin / auth) events.
	OrgID       string    `json:"orgId,omitempty"`
	ActorUserID string    `json:"actorUserId,omitempty"`
	ActorEmail  string    `json:"actorEmail,omitempty"`
	Action      string    `json:"action"`               // e.g. "plan.create", "secret.set", "auth.login"
	TargetType  string    `json:"targetType,omitempty"` // e.g. "plan", "app_env", "member"
	TargetID    string    `json:"targetId,omitempty"`   // the affected id/key (never a secret value)
	Metadata    string    `json:"metadata,omitempty"`   // small JSON/string detail (never a secret value)
	At          time.Time `json:"at"`
}

// AuditFilter scopes an audit-log listing. An empty OrgID lists platform-level
// (super-admin) events; a set OrgID lists that org's events. Limit caps the
// number of (most-recent-first) rows; <=0 falls back to a default. Offset skips
// that many leading rows for keyset-free pagination.
type AuditFilter struct {
	OrgID  string
	Limit  int
	Offset int
}

// AppEnvEntry is a single app environment variable / secret with its at-rest
// representation. Secret entries carry an encrypted (or, in dev, plaintext)
// Value and are masked before they leave the API.
type AppEnvEntry struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Secret bool   `json:"secret"`
}

// RefreshToken is a persisted record of an issued refresh token, keyed by the
// token's jti claim. It backs refresh-token rotation and revocation: a token is
// only honored while a matching, non-revoked record exists. On rotation the old
// record is revoked and a new one stored, so a revoked or unknown jti is treated
// as a reuse attempt and rejected.
type RefreshToken struct {
	ID        string    `json:"id"` // jti embedded in the refresh JWT
	UserID    string    `json:"userId"`
	Revoked   bool      `json:"revoked"`
	CreatedAt time.Time `json:"createdAt"`
	// ExpiresAt is the moment the stored refresh token can no longer be used. A
	// zero value means "no stored expiry" (back-compat for rows created before
	// expiry tracking); such rows fall back to the JWT's own exp claim.
	ExpiresAt time.Time `json:"expiresAt,omitempty"`
}

// ApiToken is a personal access token (PAT): a long-lived credential a user
// issues to authenticate API/CLI requests without a browser session. The
// plaintext token ("vrt_<random>") is shown to the user EXACTLY ONCE at
// creation and never stored; only its SHA-256 hash (TokenHash) is persisted, so
// a database leak yields no usable tokens. A request bearing "Authorization:
// Bearer vrt_<token>" authenticates as the token's owner (UserID). The token is
// valid while it exists and is unexpired (ExpiresAt zero => never expires).
type ApiToken struct {
	ID        string `json:"id"`
	UserID    string `json:"userId"`
	Name      string `json:"name"`
	TokenHash string `json:"-"` // SHA-256 hex of the full token; NEVER the plaintext
	// Prefix is the first 8 chars of the plaintext token (e.g. "vrt_ab12"),
	// stored for display so a user can recognize a token in a listing without
	// the secret ever being revealed.
	Prefix string   `json:"prefix"`
	Scopes []string `json:"scopes"`
	// ExpiresAt is when the token can no longer authenticate. A zero value means
	// "never expires".
	ExpiresAt time.Time `json:"expiresAt,omitempty"`
	// LastUsedAt is a best-effort record of the last time the token authenticated
	// a request, updated asynchronously. Zero until first use.
	LastUsedAt time.Time `json:"lastUsedAt,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
}

// PasswordResetToken is a persisted, single-use, time-limited credential backing
// the password-reset flow. The plaintext token is emailed to the user and never
// stored; only its SHA-256 hash (TokenHash) is persisted, so a database leak does
// not yield usable reset tokens. A token is valid while it is unconsumed
// (UsedAt zero) and unexpired (now < ExpiresAt).
type PasswordResetToken struct {
	ID        string    `json:"id"`
	UserID    string    `json:"userId"`
	TokenHash string    `json:"-"` // SHA-256 hex of the emailed token; never the plaintext
	ExpiresAt time.Time `json:"expiresAt"`
	UsedAt    time.Time `json:"usedAt,omitempty"` // zero until consumed
	CreatedAt time.Time `json:"createdAt"`
}
