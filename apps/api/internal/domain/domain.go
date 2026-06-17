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
	CreatedAt    time.Time `json:"createdAt"`
}

// Organization is the tenancy boundary that owns apps, databases and billing.
type Organization struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	CreatedAt time.Time `json:"createdAt"`
}

// Membership links a user to an organization with a role.
type Membership struct {
	OrgID  string `json:"orgId"`
	UserID string `json:"userId"`
	Role   Role   `json:"role"`
}

// Project groups apps and databases within an organization (Org → Project → App),
// mirroring Coolify's project concept. Every org has at least a "default" project.
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
}

// App is a Viro application owned by an organization and grouped under a project.
// It mirrors a Coolify application (CoolifyUUID) but is the tenant-scoped record
// Viro authorizes against.
type App struct {
	ID            string    `json:"id"`
	OrgID         string    `json:"orgId"`
	ProjectID     string    `json:"projectId"`
	CoolifyUUID   string    `json:"coolifyUuid,omitempty"`
	Name          string    `json:"name"`
	GitRepository string    `json:"gitRepository,omitempty"`
	GitBranch     string    `json:"gitBranch,omitempty"`
	BuildPack     string    `json:"buildPack,omitempty"`
	CPU           float64   `json:"cpu"`      // requested vCPU
	MemoryMB      int       `json:"memoryMb"` // requested memory in MB
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"createdAt"`
}

// Service is a one-click catalog instance (WordPress, a database, etc.) owned by
// an organization and grouped under a project. Provisioned via Coolify when
// configured; managed as a store record in demo mode.
type Service struct {
	ID          string    `json:"id"`
	OrgID       string    `json:"orgId"`
	ProjectID   string    `json:"projectId"`
	Template    string    `json:"template"` // catalog template key
	Name        string    `json:"name"`
	CoolifyUUID string    `json:"coolifyUuid,omitempty"`
	CPU         float64   `json:"cpu"`
	MemoryMB    int       `json:"memoryMb"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"createdAt"`
}

// Domain is a custom domain (FQDN) attached to an app.
type Domain struct {
	ID        string    `json:"id"`
	OrgID     string    `json:"orgId"`
	AppID     string    `json:"appId"`
	Domain    string    `json:"domain"`
	Verified  bool      `json:"verified"`
	CreatedAt time.Time `json:"createdAt"`
}

// Database is a Viro managed database owned by an organization, mirroring a
// Coolify standalone database.
type Database struct {
	ID          string    `json:"id"`
	OrgID       string    `json:"orgId"`
	CoolifyUUID string    `json:"coolifyUuid,omitempty"`
	Name        string    `json:"name"`
	Engine      string    `json:"engine"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"createdAt"`
}

// Plan is a billing plan in the Viro catalog (fly.io-style usage-based pricing).
type Plan struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	Description         string `json:"description"`
	PriceCents          int    `json:"priceCents"`    // monthly base price
	Currency            string `json:"currency"`      // e.g. "usd"
	IncludedHours       int    `json:"includedHours"` // included compute-hours per month
	OveragePerHourCents int    `json:"overagePerHourCents"`
	StripePriceID       string `json:"-"` // mapped to a Stripe price when billing is live
}

// SubscriptionStatus mirrors the lifecycle of a subscription.
type SubscriptionStatus string

const (
	SubActive     SubscriptionStatus = "active"
	SubTrialing   SubscriptionStatus = "trialing"
	SubIncomplete SubscriptionStatus = "incomplete"
	SubCanceled   SubscriptionStatus = "canceled"
)

// Subscription is an organization's billing subscription.
type Subscription struct {
	OrgID                string             `json:"orgId"`
	PlanID               string             `json:"planId"`
	Status               SubscriptionStatus `json:"status"`
	StripeCustomerID     string             `json:"-"`
	StripeSubscriptionID string             `json:"-"`
	CreatedAt            time.Time          `json:"createdAt"`
	CurrentPeriodEnd     time.Time          `json:"currentPeriodEnd"`
}

// UsageRecord is a metered usage event for an organization.
type UsageRecord struct {
	ID       string    `json:"id"`
	OrgID    string    `json:"orgId"`
	Metric   string    `json:"metric"` // e.g. "compute_hours", "builds", "egress_gb"
	Quantity int64     `json:"quantity"`
	At       time.Time `json:"at"`
}
