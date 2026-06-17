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
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/billing"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/coolify"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/store"
)

// ErrNotFound is returned when a resource does not exist within the org.
var ErrNotFound = errors.New("platform: not found")

// ErrQuotaExceeded is returned when a requested workload exceeds the org's plan.
var ErrQuotaExceeded = errors.New("platform: plan quota exceeded")

// ErrInvalidTemplate is returned when a catalog template key is unknown.
var ErrInvalidTemplate = errors.New("platform: unknown catalog template")

// Default resource request for a workload when the caller leaves it unset.
const (
	defaultCPU      = 0.25
	defaultMemoryMB = 256
)

// planLimits returns the resource limits for the org's plan (hobby if none).
func (s *Service) planLimits(ctx context.Context, orgID string) billing.Limits {
	planID := "hobby"
	if sub, err := s.store.GetSubscription(ctx, orgID); err == nil && sub != nil {
		planID = sub.PlanID
	}
	return billing.PlanLimits(planID)
}

// normalizeResources applies defaults to a workload's resource request.
func normalizeResources(cpu float64, memMB int) (float64, int) {
	if cpu <= 0 {
		cpu = defaultCPU
	}
	if memMB <= 0 {
		memMB = defaultMemoryMB
	}
	return cpu, memMB
}

// checkQuota validates a requested workload (cpu/memory and total count) against
// the org's plan limits.
func (s *Service) checkQuota(ctx context.Context, orgID string, cpu float64, memMB, currentCount int) error {
	lim := s.planLimits(ctx, orgID)
	if cpu > lim.MaxCPU {
		return fmt.Errorf("%w: cpu %.2f exceeds plan max %.2f", ErrQuotaExceeded, cpu, lim.MaxCPU)
	}
	if memMB > lim.MaxMemoryMB {
		return fmt.Errorf("%w: memory %dMB exceeds plan max %dMB", ErrQuotaExceeded, memMB, lim.MaxMemoryMB)
	}
	if currentCount >= lim.MaxApps {
		return fmt.Errorf("%w: workload count %d reaches plan max %d", ErrQuotaExceeded, currentCount, lim.MaxApps)
	}
	return nil
}

// workloadCount returns the org's current app + service count (counts against MaxApps).
func (s *Service) workloadCount(ctx context.Context, orgID string) (int, error) {
	apps, err := s.store.ListAppsByOrg(ctx, orgID)
	if err != nil {
		return 0, err
	}
	svcs, err := s.store.ListServicesByOrg(ctx, orgID)
	if err != nil {
		return 0, err
	}
	return len(apps) + len(svcs), nil
}

// memoryLimitString renders a memory limit in Coolify's "<n>M" form.
func memoryLimitString(memMB int) string { return fmt.Sprintf("%dM", memMB) }

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
	ProjectID     string // Viro project the app belongs to (Org → Project → App)
	GitRepository string
	GitBranch     string
	BuildPack     string
	CPU           float64 // requested vCPU (defaulted when 0)
	MemoryMB      int     // requested memory in MB (defaulted when 0)
	ProjectUUID   string  // Coolify project placement (optional)
	ServerUUID    string
}

// CreateApp creates an app for the org, provisioning it in Coolify when configured.
// Requested CPU/memory are validated against the org's plan quota.
func (s *Service) CreateApp(ctx context.Context, orgID string, in CreateAppInput) (*domain.App, error) {
	branch := in.GitBranch
	if branch == "" {
		branch = "main"
	}
	cpu, memMB := normalizeResources(in.CPU, in.MemoryMB)

	count, err := s.workloadCount(ctx, orgID)
	if err != nil {
		return nil, err
	}
	if err := s.checkQuota(ctx, orgID, cpu, memMB, count); err != nil {
		return nil, err
	}

	app := &domain.App{
		ID:            s.idgen(),
		OrgID:         orgID,
		ProjectID:     in.ProjectID,
		Name:          strings.TrimSpace(in.Name),
		GitRepository: in.GitRepository,
		GitBranch:     branch,
		BuildPack:     in.BuildPack,
		CPU:           cpu,
		MemoryMB:      memMB,
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
			LimitsCPUs:    cpu,
			LimitsMemory:  memoryLimitString(memMB),
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

// AppLogs returns recent logs for an org's app (empty in demo mode / no Coolify).
func (s *Service) AppLogs(ctx context.Context, orgID, appID string) (string, error) {
	app, err := s.ownedApp(ctx, orgID, appID)
	if err != nil {
		return "", err
	}
	if !s.coolify.Configured() || app.CoolifyUUID == "" {
		return "", nil
	}
	return s.coolify.GetApplicationLogs(ctx, app.CoolifyUUID)
}

// ListApps returns the apps belonging to the org.
func (s *Service) ListApps(ctx context.Context, orgID string) ([]domain.App, error) {
	return s.store.ListAppsByOrg(ctx, orgID)
}

// ListAppsInProject returns the org's apps filtered to a single project.
func (s *Service) ListAppsInProject(ctx context.Context, orgID, projectID string) ([]domain.App, error) {
	all, err := s.store.ListAppsByOrg(ctx, orgID)
	if err != nil {
		return nil, err
	}
	out := make([]domain.App, 0, len(all))
	for _, a := range all {
		if a.ProjectID == projectID {
			out = append(out, a)
		}
	}
	return out, nil
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
