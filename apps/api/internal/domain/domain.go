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
