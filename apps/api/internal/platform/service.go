// Package platform implements tenant-scoped app and service lifecycle on top of
// the Kubernetes deploy backend (kube.Backend). Every operation is scoped to an
// organization; the HTTP layer is responsible for authorizing the caller's
// membership/role before calling in.
//
// Workloads are placed into a per-org-project namespace and installed as Helm
// releases by the backend. There is no demo / no-op success path: tests inject
// kube.FakeBackend (a real, inspectable in-memory double) rather than skipping
// the backend.
package platform

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/billing"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/catalog"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/kube"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/store"
)

// ErrNotFound is returned when a resource does not exist within the org.
var ErrNotFound = errors.New("platform: not found")

// ErrQuotaExceeded is returned when a requested workload exceeds the org's plan.
var ErrQuotaExceeded = errors.New("platform: plan quota exceeded")

// ErrInvalidTemplate is returned when a catalog template key is unknown.
var ErrInvalidTemplate = errors.New("platform: unknown catalog template")

// planLimits returns the resource limits for the org's plan, reading the plan
// (and its Max* quotas) from the store via the billing service. An org with no
// subscription falls back to the store's default plan.
func (s *Service) planLimits(ctx context.Context, orgID string) billing.Limits {
	planID := ""
	if sub, err := s.store.GetSubscription(ctx, orgID); err == nil && sub != nil {
		planID = sub.PlanID
	}
	return s.billing.PlanLimits(ctx, planID)
}

// normalizeResources applies the platform default CPU/memory (from settings) to
// a workload's resource request when the caller leaves them unset.
func (s *Service) normalizeResources(ctx context.Context, cpu float64, memMB int) (float64, int) {
	defCPU, defMem := 0.25, 256
	if set, err := s.store.GetSettings(ctx); err == nil && set != nil {
		defCPU, defMem = set.DefaultCPU, set.DefaultMemoryMB
	}
	if cpu <= 0 {
		cpu = defCPU
	}
	if memMB <= 0 {
		memMB = defMem
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

// Service provides org-scoped app and service operations on top of the
// Kubernetes deploy backend.
type Service struct {
	store   store.Store
	backend kube.Backend
	billing *billing.Service
	idgen   func() string
	now     func() time.Time
}

// NewService builds a platform service. The backend is the Kubernetes deploy
// surface (kube.Backend); tests inject kube.FakeBackend. The billing service
// supplies store-backed plan limits for quota enforcement.
func NewService(s store.Store, backend kube.Backend, b *billing.Service) *Service {
	if b == nil {
		b = billing.NewService(s, nil)
	}
	if backend == nil {
		backend = kube.NewFakeBackend()
	}
	return &Service{store: s, backend: backend, billing: b, idgen: uuid.NewString, now: time.Now}
}

// orgSlug resolves the org's slug, falling back to the org id when the org
// record is missing (e.g. in unit tests that work with bare ids).
func (s *Service) orgSlug(ctx context.Context, orgID string) string {
	if org, err := s.store.GetOrganization(ctx, orgID); err == nil && org != nil && org.Slug != "" {
		return org.Slug
	}
	return orgID
}

// projectSlug resolves a project's slug, falling back to the project id (or
// "default" when unset) when the project record is missing.
func (s *Service) projectSlug(ctx context.Context, projectID string) string {
	if projectID == "" {
		return "default"
	}
	if p, err := s.store.GetProject(ctx, projectID); err == nil && p != nil && p.Slug != "" {
		return p.Slug
	}
	return projectID
}

// quotaForOrg builds the backend tenant quota from the org's plan limits and the
// admin-configured minimal default size / overcommit factors (used for the
// namespace LimitRange). All values are live from the store — none are hardcoded.
func (s *Service) quotaForOrg(ctx context.Context, orgID string) kube.Quota {
	lim := s.planLimits(ctx, orgID)
	q := kube.Quota{MaxCPU: lim.MaxCPU, MaxMemoryMB: lim.MaxMemoryMB, MaxApps: lim.MaxApps}
	if set, err := s.store.GetSettings(ctx); err == nil && set != nil {
		q.DefaultCPU = set.DefaultCPU
		q.DefaultMemoryMB = set.DefaultMemoryMB
		q.CPUOvercommitFactor = set.CPUOvercommitFactor
		q.MemoryOvercommitFactor = set.MemoryOvercommitFactor
	}
	return q
}

// CreateAppInput describes a new application.
type CreateAppInput struct {
	Name          string
	ProjectID     string // Vortex project the app belongs to (Org → Project → App)
	Image         string // container image; when set the app deploys directly (no build)
	GitRepository string
	GitBranch     string
	BuildPack     string
	CPU           float64 // requested vCPU (defaulted when 0)
	MemoryMB      int     // requested memory in MB (defaulted when 0)
	ProjectUUID   string  // Coolify project placement (optional)
	ServerUUID    string
}

// overcommitFactors returns the live CPU/memory overcommit factors from platform
// settings (admin/DB-driven). Zero values tell the backend to use its configured
// default, so this never forces a hardcoded factor onto a deploy.
func (s *Service) overcommitFactors(ctx context.Context) (cpuFactor, memFactor float64) {
	if set, err := s.store.GetSettings(ctx); err == nil && set != nil {
		return set.CPUOvercommitFactor, set.MemoryOvercommitFactor
	}
	return 0, 0
}

// CreateApp creates an app for the org. Requested CPU/memory are validated
// against the org's plan quota, and the per-org-project namespace + quota are
// ensured on the backend.
//
// When an Image is supplied the app deploys immediately (helm upgrade --install)
// and is marked "deploying". Git-based apps without an image stay "queued" until
// the image builder produces an image (no demo success path).
func (s *Service) CreateApp(ctx context.Context, orgID string, in CreateAppInput) (*domain.App, error) {
	branch := in.GitBranch
	if branch == "" {
		branch = "main"
	}
	cpu, memMB := s.normalizeResources(ctx, in.CPU, in.MemoryMB)

	count, err := s.workloadCount(ctx, orgID)
	if err != nil {
		return nil, err
	}
	if err := s.checkQuota(ctx, orgID, cpu, memMB, count); err != nil {
		return nil, err
	}

	orgSlug := s.orgSlug(ctx, orgID)
	projSlug := s.projectSlug(ctx, in.ProjectID)

	// Ensure the tenant namespace + ResourceQuota/LimitRange exist up front, so
	// quota is enforced and the placement is ready once a build produces an image.
	namespace, err := s.backend.EnsureTenant(ctx, orgSlug, projSlug, s.quotaForOrg(ctx, orgID))
	if err != nil {
		return nil, err
	}

	app := &domain.App{
		ID:            s.idgen(),
		OrgID:         orgID,
		ProjectID:     in.ProjectID,
		Name:          strings.TrimSpace(in.Name),
		Image:         strings.TrimSpace(in.Image),
		GitRepository: in.GitRepository,
		GitBranch:     branch,
		BuildPack:     in.BuildPack,
		CPU:           cpu,
		MemoryMB:      memMB,
		Status:        "queued",
		Namespace:     namespace,
		CreatedAt:     s.now(),
	}

	if app.Image != "" {
		// Image-based app: deploy directly, no build needed.
		cpuF, memF := s.overcommitFactors(ctx)
		release, host, err := s.backend.Apply(ctx, kube.Workload{
			OrgSlug:                orgSlug,
			ProjectSlug:            projSlug,
			Name:                   app.Name,
			Kind:                   "app",
			Image:                  app.Image,
			CPU:                    cpu,
			MemoryMB:               memMB,
			CPUOvercommitFactor:    cpuF,
			MemoryOvercommitFactor: memF,
		})
		if err != nil {
			return nil, err
		}
		app.Release = release
		app.Host = host
		app.Status = "deploying"
	}
	// Git-only apps remain "queued" until the image builder produces an image.

	if err := s.store.CreateApp(ctx, app); err != nil {
		return nil, err
	}
	return app, nil
}

// AppLogs returns recent logs for an org's app from the backend (empty when the
// app has not been deployed yet, i.e. no Release).
func (s *Service) AppLogs(ctx context.Context, orgID, appID string) (string, error) {
	app, err := s.ownedApp(ctx, orgID, appID)
	if err != nil {
		return "", err
	}
	if app.Release == "" {
		return "", nil
	}
	return s.backend.Logs(ctx, app.Namespace, app.Release, 200)
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

// Deploy (re)starts a deployed app on the backend and updates status.
func (s *Service) Deploy(ctx context.Context, orgID, appID string) (*domain.App, error) {
	return s.action(ctx, orgID, appID, "deploying", s.backend.Start)
}

// Stop scales the app to zero on the backend.
func (s *Service) Stop(ctx context.Context, orgID, appID string) (*domain.App, error) {
	return s.action(ctx, orgID, appID, "stopped", s.backend.Stop)
}

// Restart triggers a rollout restart of the app on the backend.
func (s *Service) Restart(ctx context.Context, orgID, appID string) (*domain.App, error) {
	return s.action(ctx, orgID, appID, "restarting", s.backend.Restart)
}

// Delete uninstalls the app's release from the backend (when deployed) and
// removes the store record.
func (s *Service) Delete(ctx context.Context, orgID, appID string) error {
	app, err := s.ownedApp(ctx, orgID, appID)
	if err != nil {
		return err
	}
	if app.Release != "" {
		if err := s.backend.Delete(ctx, app.Namespace, app.Release); err != nil {
			return err
		}
	}
	return s.store.DeleteApp(ctx, app.ID)
}

// action applies a status transition, invoking the backend lifecycle call for
// the app's release when it has been deployed.
func (s *Service) action(ctx context.Context, orgID, appID, status string, fn func(context.Context, string, string) error) (*domain.App, error) {
	app, err := s.ownedApp(ctx, orgID, appID)
	if err != nil {
		return nil, err
	}
	if app.Release != "" {
		if err := fn(ctx, app.Namespace, app.Release); err != nil {
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
	Name      string
	Engine    string // postgresql, mysql, mariadb, mongodb, redis, ...
	ProjectID string // tenant project; defaults to the org's default project
	CPU       float64
	MemoryMB  int
}

// CreateDatabase provisions a managed database for the org: it resolves the
// engine to a catalog template (image/port, DB/admin-driven), enforces the
// plan quota, ensures the tenant namespace, and deploys the engine as a
// StatefulSet via the backend. The Kubernetes placement is persisted and the
// database is marked "deploying".
func (s *Service) CreateDatabase(ctx context.Context, orgID string, in CreateDatabaseInput) (*domain.Database, error) {
	engine := strings.ToLower(strings.TrimSpace(in.Engine))
	if engine == "" {
		engine = "postgresql"
	}
	tmpl, ok := s.templateByKey(ctx, engine)
	if !ok || catalog.Kind(tmpl.Kind) != catalog.KindDatabase {
		return nil, fmt.Errorf("%w: %q", ErrInvalidTemplate, engine)
	}

	cpu, memMB := s.normalizeResources(ctx, in.CPU, in.MemoryMB)
	count, err := s.workloadCount(ctx, orgID)
	if err != nil {
		return nil, err
	}
	if err := s.checkQuota(ctx, orgID, cpu, memMB, count); err != nil {
		return nil, err
	}

	name := strings.TrimSpace(in.Name)
	orgSlug := s.orgSlug(ctx, orgID)
	projSlug := s.projectSlug(ctx, in.ProjectID)

	namespace, err := s.backend.EnsureTenant(ctx, orgSlug, projSlug, s.quotaForOrg(ctx, orgID))
	if err != nil {
		return nil, err
	}

	cpuF, memF := s.overcommitFactors(ctx)
	release, host, err := s.backend.Apply(ctx, kube.Workload{
		OrgSlug:                orgSlug,
		ProjectSlug:            projSlug,
		Name:                   name,
		Kind:                   "database",
		Image:                  tmpl.Image,
		Port:                   tmpl.DefaultPort,
		CPU:                    cpu,
		MemoryMB:               memMB,
		ServiceTemplateKey:     tmpl.Key,
		CPUOvercommitFactor:    cpuF,
		MemoryOvercommitFactor: memF,
	})
	if err != nil {
		return nil, err
	}

	db := &domain.Database{
		ID:        s.idgen(),
		OrgID:     orgID,
		ProjectID: in.ProjectID,
		Name:      name,
		Engine:    engine,
		CPU:       cpu,
		MemoryMB:  memMB,
		Status:    "deploying",
		Namespace: namespace,
		Release:   release,
		Host:      host,
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

// GetDatabase returns one database scoped to the org.
func (s *Service) GetDatabase(ctx context.Context, orgID, dbID string) (*domain.Database, error) {
	return s.ownedDatabase(ctx, orgID, dbID)
}

// DeleteDatabase uninstalls the database's release from the backend (when
// deployed) and removes the store record.
func (s *Service) DeleteDatabase(ctx context.Context, orgID, dbID string) error {
	db, err := s.ownedDatabase(ctx, orgID, dbID)
	if err != nil {
		return err
	}
	if db.Release != "" {
		if err := s.backend.Delete(ctx, db.Namespace, db.Release); err != nil {
			return err
		}
	}
	return s.store.DeleteDatabase(ctx, db.ID)
}

func (s *Service) ownedDatabase(ctx context.Context, orgID, dbID string) (*domain.Database, error) {
	db, err := s.store.GetDatabase(ctx, dbID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if db.OrgID != orgID {
		return nil, ErrNotFound
	}
	return db, nil
}
