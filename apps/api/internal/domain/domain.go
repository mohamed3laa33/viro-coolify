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

// App is a Viro application owned by an organization. It mirrors a Coolify
// application (CoolifyUUID) but is the tenant-scoped record Viro authorizes against.
type App struct {
	ID            string    `json:"id"`
	OrgID         string    `json:"orgId"`
	CoolifyUUID   string    `json:"coolifyUuid,omitempty"`
	Name          string    `json:"name"`
	GitRepository string    `json:"gitRepository,omitempty"`
	GitBranch     string    `json:"gitBranch,omitempty"`
	BuildPack     string    `json:"buildPack,omitempty"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"createdAt"`
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
	ID            string `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	PriceCents    int    `json:"priceCents"`     // monthly base price
	Currency      string `json:"currency"`       // e.g. "usd"
	IncludedHours int    `json:"includedHours"`  // included compute-hours per month
	OveragePerHourCents int `json:"overagePerHourCents"`
	StripePriceID string `json:"-"`              // mapped to a Stripe price when billing is live
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
