// Package platform implements tenant-scoped app and database lifecycle on top
// of Coolify. Every operation is scoped to an organization; the HTTP layer is
// responsible for authorizing the caller's membership/role before calling in.
//
// When Coolify is not configured (local/demo mode), records are managed in the
// store and outbound Coolify calls are skipped so the product is fully usable
// locally.
package platform

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/coolify"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/store"
)

// ErrNotFound is returned when a resource does not exist within the org.
var ErrNotFound = errors.New("platform: not found")

// Service provides org-scoped app and database operations.
type Service struct {
	store   store.Store
	coolify *coolify.Client
	idgen   func() string
	now     func() time.Time
}

// NewService builds a platform service.
func NewService(s store.Store, c *coolify.Client) *Service {
	return &Service{store: s, coolify: c, idgen: uuid.NewString, now: time.Now}
}

// CreateAppInput describes a new application.
type CreateAppInput struct {
	Name          string
	GitRepository string
	GitBranch     string
	BuildPack     string
	ProjectUUID   string
	ServerUUID    string
}

// CreateApp creates an app for the org, provisioning it in Coolify when configured.
func (s *Service) CreateApp(ctx context.Context, orgID string, in CreateAppInput) (*domain.App, error) {
	branch := in.GitBranch
	if branch == "" {
		branch = "main"
	}
	app := &domain.App{
		ID:            s.idgen(),
		OrgID:         orgID,
		Name:          strings.TrimSpace(in.Name),
		GitRepository: in.GitRepository,
		GitBranch:     branch,
		BuildPack:     in.BuildPack,
		Status:        "created",
		CreatedAt:     s.now(),
	}

	if s.coolify.Configured() && in.GitRepository != "" {
		coolifyUUID, err := s.coolify.CreatePublicApplication(ctx, coolify.CreatePublicApplicationRequest{
			ProjectUUID:   in.ProjectUUID,
			ServerUUID:    in.ServerUUID,
			GitRepository: in.GitRepository,
			GitBranch:     branch,
			BuildPack:     in.BuildPack,
			Name:          app.Name,
		})
		if err != nil {
			return nil, err
		}
		app.CoolifyUUID = coolifyUUID
	}

	if err := s.store.CreateApp(ctx, app); err != nil {
		return nil, err
	}
	return app, nil
}

// ListApps returns the apps belonging to the org.
func (s *Service) ListApps(ctx context.Context, orgID string) ([]domain.App, error) {
	return s.store.ListAppsByOrg(ctx, orgID)
}

// GetApp returns one app, ensuring it belongs to the org.
func (s *Service) GetApp(ctx context.Context, orgID, appID string) (*domain.App, error) {
	return s.ownedApp(ctx, orgID, appID)
}

// Deploy (re)deploys the app via Coolify (when configured) and updates status.
func (s *Service) Deploy(ctx context.Context, orgID, appID string) (*domain.App, error) {
	return s.action(ctx, orgID, appID, "deploying", s.coolify.StartApplication)
}

// Stop stops the app.
func (s *Service) Stop(ctx context.Context, orgID, appID string) (*domain.App, error) {
	return s.action(ctx, orgID, appID, "stopped", s.coolify.StopApplication)
}

// Restart restarts the app.
func (s *Service) Restart(ctx context.Context, orgID, appID string) (*domain.App, error) {
	return s.action(ctx, orgID, appID, "restarting", s.coolify.RestartApplication)
}

// Delete removes the app from Coolify (when configured) and the store.
func (s *Service) Delete(ctx context.Context, orgID, appID string) error {
	app, err := s.ownedApp(ctx, orgID, appID)
	if err != nil {
		return err
	}
	if s.coolify.Configured() && app.CoolifyUUID != "" {
		if err := s.coolify.DeleteApplication(ctx, app.CoolifyUUID); err != nil {
			return err
		}
	}
	return s.store.DeleteApp(ctx, app.ID)
}

func (s *Service) action(ctx context.Context, orgID, appID, status string, fn func(context.Context, string) error) (*domain.App, error) {
	app, err := s.ownedApp(ctx, orgID, appID)
	if err != nil {
		return nil, err
	}
	if s.coolify.Configured() && app.CoolifyUUID != "" {
		if err := fn(ctx, app.CoolifyUUID); err != nil {
			return nil, err
		}
	}
	app.Status = status
	if err := s.store.UpdateApp(ctx, app); err != nil {
		return nil, err
	}
	return app, nil
}

func (s *Service) ownedApp(ctx context.Context, orgID, appID string) (*domain.App, error) {
	app, err := s.store.GetApp(ctx, appID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if app.OrgID != orgID {
		// Do not leak existence across tenants.
		return nil, ErrNotFound
	}
	return app, nil
}

// CreateDatabaseInput describes a new managed database.
type CreateDatabaseInput struct {
	Name   string
	Engine string // postgresql, mysql, mariadb, mongodb, redis, ...
}

// CreateDatabase creates a managed database record for the org.
func (s *Service) CreateDatabase(ctx context.Context, orgID string, in CreateDatabaseInput) (*domain.Database, error) {
	engine := strings.ToLower(strings.TrimSpace(in.Engine))
	if engine == "" {
		engine = "postgresql"
	}
	db := &domain.Database{
		ID:        s.idgen(),
		OrgID:     orgID,
		Name:      strings.TrimSpace(in.Name),
		Engine:    engine,
		Status:    "created",
		CreatedAt: s.now(),
	}
	if err := s.store.CreateDatabase(ctx, db); err != nil {
		return nil, err
	}
	return db, nil
}

// ListDatabases returns the databases belonging to the org.
func (s *Service) ListDatabases(ctx context.Context, orgID string) ([]domain.Database, error) {
	return s.store.ListDatabasesByOrg(ctx, orgID)
}
