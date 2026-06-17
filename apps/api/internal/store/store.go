// Package store defines Viro's persistence interfaces and ships an in-memory
// implementation. A Postgres implementation satisfies the same interface.
package store

import (
	"context"
	"errors"

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

	// Organizations.
	CreateOrganization(ctx context.Context, o *domain.Organization) error
	GetOrganization(ctx context.Context, id string) (*domain.Organization, error)
	ListOrganizationsForUser(ctx context.Context, userID string) ([]domain.Organization, error)

	// Memberships.
	AddMembership(ctx context.Context, m domain.Membership) error
	GetMembership(ctx context.Context, orgID, userID string) (*domain.Membership, error)
	ListMemberships(ctx context.Context, orgID string) ([]domain.Membership, error)

	// Apps (tenant-scoped).
	CreateApp(ctx context.Context, a *domain.App) error
	GetApp(ctx context.Context, id string) (*domain.App, error)
	ListAppsByOrg(ctx context.Context, orgID string) ([]domain.App, error)
	UpdateApp(ctx context.Context, a *domain.App) error
	DeleteApp(ctx context.Context, id string) error

	// Databases (tenant-scoped).
	CreateDatabase(ctx context.Context, d *domain.Database) error
	ListDatabasesByOrg(ctx context.Context, orgID string) ([]domain.Database, error)
}
