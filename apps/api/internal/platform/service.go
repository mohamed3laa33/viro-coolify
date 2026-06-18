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
	"io"
	"log"
	"net"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/billing"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/build"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/catalog"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/kube"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/secrets"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/store"
)

// ErrNotFound is returned when a resource does not exist within the org.
var ErrNotFound = errors.New("platform: not found")

// ErrQuotaExceeded is returned when a requested workload exceeds the org's plan.
var ErrQuotaExceeded = errors.New("platform: plan quota exceeded")

// ErrInvalidTemplate is returned when a catalog template key is unknown.
var ErrInvalidTemplate = errors.New("platform: unknown catalog template")

// ErrNoImage is returned when a deploy is requested for an app that has no image
// yet (e.g. a git-based app whose build has not produced an image). The HTTP
// layer maps it to 409/422 rather than faking a successful deploy.
var ErrNoImage = errors.New("platform: no image to deploy yet — build pending")

// ErrPaymentRequired is returned when an org's subscription does not permit new
// provisioning/deploys (canceled/unpaid/past_due without grace, or over the spend
// cap). It wraps billing.ErrPaymentRequired so the HTTP layer maps it to 402.
var ErrPaymentRequired = billing.ErrPaymentRequired

// ensureActive gates revenue-affecting operations on the org's subscription state
// and spend cap (admin/DB-driven). It returns ErrPaymentRequired when the org may
// not provision/deploy.
func (s *Service) ensureActive(ctx context.Context, orgID string) error {
	return s.billing.EnsureActive(ctx, orgID)
}

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

// checkWorkloadSize re-validates an EXISTING workload's cpu/memory against the
// org's current plan ceilings (used on Deploy/Restart, so a workload sized under
// an old plan can't be redeployed/restarted beyond a downgraded plan). It does not
// apply the MaxApps count check (the workload already exists).
func (s *Service) checkWorkloadSize(ctx context.Context, orgID string, cpu float64, memMB int) error {
	lim := s.planLimits(ctx, orgID)
	if cpu > lim.MaxCPU {
		return fmt.Errorf("%w: cpu %.2f exceeds plan max %.2f", ErrQuotaExceeded, cpu, lim.MaxCPU)
	}
	if memMB > lim.MaxMemoryMB {
		return fmt.Errorf("%w: memory %dMB exceeds plan max %dMB", ErrQuotaExceeded, memMB, lim.MaxMemoryMB)
	}
	return nil
}

// workloadCount returns the org's current app + service + database count (all
// count against MaxApps).
func (s *Service) workloadCount(ctx context.Context, orgID string) (int, error) {
	apps, err := s.store.ListAppsByOrg(ctx, orgID)
	if err != nil {
		return 0, err
	}
	svcs, err := s.store.ListServicesByOrg(ctx, orgID)
	if err != nil {
		return 0, err
	}
	dbs, err := s.store.ListDatabasesByOrg(ctx, orgID)
	if err != nil {
		return 0, err
	}
	return len(apps) + len(svcs) + len(dbs), nil
}

// Service provides org-scoped app and service operations on top of the
// Kubernetes deploy backend.
type Service struct {
	store   store.Store
	backend kube.Backend
	builder build.Builder
	billing *billing.Service
	idgen   func() string
	now     func() time.Time

	// buildRegistry is the push target prefix (host/repo) for built images, e.g.
	// "ghcr.io/acme". Empty in dev; the target image ref is still computed so the
	// FakeBuilder flow works end-to-end.
	buildRegistry string
	// pullSecretName is the tenant-namespace imagePullSecret name attached to BUILT
	// apps so a private built image can be pulled. Empty when no registry is
	// configured (local/dev) so public/FakeBuilder flows skip the pull secret.
	pullSecretName string
	// buildTimeout bounds the async build worker's detached context.
	buildTimeout time.Duration

	// dbStorageGB is the default persistent-volume size (GiB) for a managed
	// database when the create request omits it. Zero falls back to
	// defaultDBStorageGB. Admin-tunable via VORTEX_DB_DEFAULT_STORAGE_GB.
	dbStorageGB int
	// dbStorageClass optionally overrides the StorageClass for managed-database
	// data volumes. Empty leaves the cluster/chart default.
	dbStorageClass string
	// buildWG, when set, tracks in-flight async builds so a graceful shutdown can
	// Wait() for them to drain (mirrors the metering/reconciler WaitGroup).
	buildWG *sync.WaitGroup

	// cipher encrypts/decrypts SECRET app env values at rest. Defaults to a no-op
	// pass-through (dev) when no key is configured, so the system never panics for
	// a missing key.
	cipher secrets.Cipher

	// baseDomain is the platform apex (e.g. "vortex.v60ai.com"). A tenant may NOT
	// claim it or any subdomain of it as a custom domain (that would hijack a
	// platform/other-tenant host), and it backs the CNAME target hint returned to
	// the user. Empty disables the apex guard (dev/tests still validate the FQDN).
	baseDomain string
	// gatewayLBHost is an optional explicit A/ALIAS target (the shared Gateway
	// LoadBalancer hostname/IP) advertised in the DNS instructions. When empty the
	// instructions advise a CNAME to the app's generated host instead.
	gatewayLBHost string

	// resolver looks up the DNS TXT challenge record. Overridable in tests via
	// WithResolver so verification can be exercised without real DNS.
	resolver Resolver
}

// Resolver is the minimal DNS surface VerifyDomain needs: TXT record lookup. The
// stdlib *net.Resolver satisfies it; tests inject a fake to avoid real DNS.
type Resolver interface {
	LookupTXT(ctx context.Context, name string) ([]string, error)
}

// Option customizes Service construction.
type Option func(*Service)

// WithBuilder injects the image builder used for git-source apps. When unset the
// service defaults to build.NewFakeBuilder() (mirroring kube.FakeBackend), so
// tests and no-cluster boots still exercise the full git→build→deploy flow.
func WithBuilder(b build.Builder) Option {
	return func(s *Service) {
		if b != nil {
			s.builder = b
		}
	}
}

// WithBuildRegistry sets the push target prefix for built images.
func WithBuildRegistry(reg string) Option {
	return func(s *Service) { s.buildRegistry = strings.TrimSpace(reg) }
}

// WithPullSecretName sets the tenant-namespace imagePullSecret name attached to
// built apps (and ensured per tenant namespace before deploy). Empty disables it.
func WithPullSecretName(name string) Option {
	return func(s *Service) { s.pullSecretName = strings.TrimSpace(name) }
}

// WithBuildTimeout bounds the async build worker's detached context.
func WithBuildTimeout(d time.Duration) Option {
	return func(s *Service) {
		if d > 0 {
			s.buildTimeout = d
		}
	}
}

// WithBuildWaitGroup registers a WaitGroup so in-flight async builds drain on
// shutdown.
func WithBuildWaitGroup(wg *sync.WaitGroup) Option {
	return func(s *Service) { s.buildWG = wg }
}

// WithDBStorageDefault sets the default persistent-volume size (GiB) for managed
// databases. Non-positive values are ignored (the built-in default stands).
func WithDBStorageDefault(gb int) Option {
	return func(s *Service) {
		if gb > 0 {
			s.dbStorageGB = gb
		}
	}
}

// WithDBStorageClass sets the StorageClass for managed-database data volumes.
// Empty is ignored (cluster/chart default stands).
func WithDBStorageClass(class string) Option {
	return func(s *Service) { s.dbStorageClass = strings.TrimSpace(class) }
}

// WithCipher injects the at-rest cipher used to encrypt/decrypt SECRET app env.
// A nil cipher is ignored (the no-op default stands).
func WithCipher(c secrets.Cipher) Option {
	return func(s *Service) {
		if c != nil {
			s.cipher = c
		}
	}
}

// WithBaseDomain sets the platform apex used to (a) reject tenant claims on the
// platform host / its subdomains and (b) build the custom-domain DNS instructions.
func WithBaseDomain(d string) Option {
	return func(s *Service) { s.baseDomain = strings.ToLower(strings.TrimSpace(d)) }
}

// WithGatewayLBHost sets the explicit shared-Gateway LoadBalancer host/IP
// advertised as the A/ALIAS target in custom-domain DNS instructions. Empty
// leaves a CNAME-to-generated-host instruction.
func WithGatewayLBHost(h string) Option {
	return func(s *Service) { s.gatewayLBHost = strings.TrimSpace(h) }
}

// WithResolver overrides the DNS resolver used for TXT-challenge verification
// (tests inject a fake). A nil resolver is ignored (the stdlib default stands).
func WithResolver(r Resolver) Option {
	return func(s *Service) {
		if r != nil {
			s.resolver = r
		}
	}
}

// NewService builds a platform service. The backend is the Kubernetes deploy
// surface (kube.Backend); tests inject kube.FakeBackend. The builder is the
// git-source image builder (build.Builder); tests inject build.FakeBuilder. The
// billing service supplies store-backed plan limits for quota enforcement.
func NewService(s store.Store, backend kube.Backend, b *billing.Service, opts ...Option) *Service {
	if b == nil {
		b = billing.NewService(s, nil)
	}
	if backend == nil {
		backend = kube.NewFakeBackend()
	}
	svc := &Service{
		store:        s,
		backend:      backend,
		builder:      build.NewFakeBuilder(),
		billing:      b,
		idgen:        uuid.NewString,
		now:          time.Now,
		buildTimeout: 10 * time.Minute,
		cipher:       secrets.NoopCipher{},
		resolver:     net.DefaultResolver,
	}
	for _, opt := range opts {
		opt(svc)
	}
	return svc
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

// appSecretName returns the per-app Kubernetes Secret name that holds the app's
// SECRET env, injected into the workload via envFrom secretRef.
func appSecretName(appID string) string {
	return "vortex-env-" + kubeSanitize(appID)
}

// encValuePrefix marks a value that was written by the AES-GCM cipher
// (secrets.encPrefix). A value carrying this prefix is REAL encrypted material:
// if the configured (enabled) cipher cannot Open it, the key is wrong/rotated —
// an operator error worth surfacing rather than silently deploying without it.
const encValuePrefix = "v1:"

// resolvedEnv splits an app's stored env into NON-secret config (returned as
// plain values for deployment.env) and SECRET values (DECRYPTED, for the per-app
// Kubernetes Secret). Secret values are decrypted on this deploy path only and
// never returned over the API.
//
// Decrypt-failure handling:
//   - When the active cipher is ENABLED and the stored value is a real encrypted
//     "v1:"-prefixed blob that fails to Open, the deploy is FAILED (error) — that
//     means a wrong/rotated key, and silently shipping without the secret would
//     mask a serious operator error.
//   - Otherwise (no-op cipher, or a legacy non-"v1:" plaintext value) a decrypt
//     failure drops that single key and continues, so a single bad value never
//     aborts the whole deploy.
//
// Every decrypt failure is logged by KEY NAME ONLY — never the value.
func (s *Service) resolvedEnv(ctx context.Context, appID string) (plain, secret map[string]string, err error) {
	plain = map[string]string{}
	secret = map[string]string{}
	entries, lerr := s.store.ListAppEnv(ctx, appID)
	if lerr != nil {
		// A failed env lookup must not silently deploy with no env — surface it.
		return nil, nil, lerr
	}
	for _, e := range entries {
		if !e.Secret {
			plain[e.Key] = e.Value
			continue
		}
		dec, derr := s.cipher.Decrypt(e.Value)
		if derr != nil {
			log.Printf("platform: decrypt secret env key %q for app %s failed: %v", e.Key, appID, derr)
			// A real encrypted value under an enabled cipher that won't decrypt is
			// an operator error (wrong/rotated key) — fail the deploy rather than
			// silently shipping without the secret.
			if s.cipher.Enabled() && strings.HasPrefix(e.Value, encValuePrefix) {
				return nil, nil, fmt.Errorf("platform: decrypt secret env key %q for app %s: %w", e.Key, appID, derr)
			}
			continue
		}
		secret[e.Key] = dec
	}
	return plain, secret, nil
}

// appDomains returns the VERIFIED custom hostnames attached to the app (in
// addition to the generated host), or nil when none are verified / the lookup
// fails. Only verified domains are routed: a pending/failed domain has not proven
// ownership and must NEVER reach the workload's HTTPRoute hostnames.
func (s *Service) appDomains(ctx context.Context, appID string) []string {
	doms, err := s.store.ListDomainsByApp(ctx, appID)
	if err != nil || len(doms) == 0 {
		return nil
	}
	out := make([]string, 0, len(doms))
	for _, d := range doms {
		if d.Domain != "" && d.IsVerified() {
			out = append(out, d.Domain)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// appWorkload renders the full kube.Workload for an app, populating its stored
// env + custom domains so they reach the pods on every Apply. For a git-BUILT app
// (it has a GitRepository) it also attaches the tenant-namespace imagePullSecret
// so the private built image can be pulled (no ImagePullBackOff). Public
// image-based apps leave the pull secret empty.
func (s *Service) appWorkload(ctx context.Context, app *domain.App, orgSlug, projSlug string) (kube.Workload, error) {
	cpuF, memF := s.overcommitFactors(ctx)
	pullSecret := ""
	if app.GitRepository != "" {
		pullSecret = s.pullSecretName
	}
	// Split stored env: non-secret config goes inline into the helm values; SECRET
	// values are decrypted into a per-app Kubernetes Secret referenced via envFrom
	// (so they are never baked into the release values). The Secret is ensured by
	// applyApp before Apply.
	plain, secret, err := s.resolvedEnv(ctx, app.ID)
	if err != nil {
		return kube.Workload{}, err
	}
	envSecretName := ""
	if len(secret) > 0 {
		envSecretName = appSecretName(app.ID)
	}
	return kube.Workload{
		OrgSlug:                orgSlug,
		ProjectSlug:            projSlug,
		Name:                   app.Name,
		Kind:                   "app",
		Image:                  app.Image,
		CPU:                    app.CPU,
		MemoryMB:               app.MemoryMB,
		Env:                    plain,
		Domains:                s.appDomains(ctx, app.ID),
		ImagePullSecret:        pullSecret,
		EnvSecretName:          envSecretName,
		CPUOvercommitFactor:    cpuF,
		MemoryOvercommitFactor: memF,
		Scaling:                s.scalingForApp(ctx, app),
	}, nil
}

// scalingForApp resolves the KEDA autoscaling config for an app from the
// admin/DB-driven platform settings, applying the app's per-app min/max overrides
// when set. A stateless app with a resolved MinReplicas of 0 scales to zero; the
// kube backend floors databases at 1 (apps are never floored here). All values are
// live from the store — none are hardcoded on this path.
func (s *Service) scalingForApp(ctx context.Context, app *domain.App) kube.Scaling {
	sc := kube.Scaling{
		MinReplicas:     1,
		MaxReplicas:     5,
		PollingInterval: 30,
		CooldownPeriod:  300,
		CPUUtilization:  70,
	}
	if set, err := s.store.GetSettings(ctx); err == nil && set != nil {
		sc.MinReplicas = set.KedaDefaultMinReplicas
		sc.MaxReplicas = set.KedaDefaultMaxReplicas
		sc.PollingInterval = set.KedaPollingInterval
		sc.CooldownPeriod = set.KedaCooldownPeriod
		sc.CPUUtilization = set.KedaCPUUtilization
		sc.HTTPTrigger = set.KedaHTTPTrigger
	}
	// Per-app overrides. MinReplicas may legitimately be 0 (scale-to-zero), so a
	// stored override of 0 is honored only when the user explicitly set bounds; we
	// treat a non-zero MaxReplicas as "this app has explicit bounds" and then take
	// MinReplicas verbatim (0 allowed). When only MinReplicas is set (>0) we apply it.
	if app.MaxReplicas > 0 {
		sc.MaxReplicas = app.MaxReplicas
		sc.MinReplicas = app.MinReplicas // 0 allowed => scale-to-zero
	} else if app.MinReplicas > 0 {
		sc.MinReplicas = app.MinReplicas
	}
	return sc
}

// applyApp injects the app's SECRET env as a per-app Kubernetes Secret (envFrom),
// then applies the workload. The Secret is upserted with the DECRYPTED values and
// deleted when no secrets remain, so secret material never lives in the helm
// release values. Plain config still flows through the workload Env.
func (s *Service) applyApp(ctx context.Context, app *domain.App, orgSlug, projSlug string) (string, string, error) {
	_, secret, err := s.resolvedEnv(ctx, app.ID)
	if err != nil {
		return "", "", err
	}
	if err := s.backend.EnsureAppSecret(ctx, app.Namespace, appSecretName(app.ID), secret); err != nil {
		return "", "", err
	}
	wl, err := s.appWorkload(ctx, app, orgSlug, projSlug)
	if err != nil {
		return "", "", err
	}
	return s.backend.Apply(ctx, wl)
}

// deployBuiltApp ensures the tenant-namespace imagePullSecret exists (so a private
// built image can be pulled) and then applies the workload (with its secret env).
// EnsureImagePullSecret no-ops when no registry/source is configured.
func (s *Service) deployBuiltApp(ctx context.Context, app *domain.App, orgSlug, projSlug string) (string, string, error) {
	if s.pullSecretName != "" {
		if err := s.backend.EnsureImagePullSecret(ctx, app.Namespace, s.pullSecretName); err != nil {
			return "", "", err
		}
	}
	return s.applyApp(ctx, app, orgSlug, projSlug)
}

// CreateApp creates an app for the org. Requested CPU/memory are validated
// against the org's plan quota, and the per-org-project namespace + quota are
// ensured on the backend.
//
// When an Image is supplied the app deploys immediately (helm upgrade --install)
// and is marked "deploying". Git-based apps without an image stay "queued" until
// the image builder produces an image (no demo success path).
func (s *Service) CreateApp(ctx context.Context, orgID string, in CreateAppInput) (*domain.App, error) {
	if err := s.ensureActive(ctx, orgID); err != nil {
		return nil, err
	}
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
		GitRepository: strings.TrimSpace(in.GitRepository),
		GitBranch:     branch,
		BuildPack:     in.BuildPack,
		CPU:           cpu,
		MemoryMB:      memMB,
		Status:        "queued",
		Namespace:     namespace,
		CreatedAt:     s.now(),
	}

	switch {
	case app.Image != "":
		// Image-based app: deploy directly, no build needed. The workload carries
		// the app's stored env + custom domains so they reach the pods.
		release, host, err := s.applyApp(ctx, app, orgSlug, projSlug)
		if err != nil {
			return nil, err
		}
		app.Release = release
		app.Host = host
		app.Status = "deploying"
		if err := s.store.CreateApp(ctx, app); err != nil {
			return nil, err
		}
		// Record the first release revision (rev1). A failure here must not fail the
		// already-successful create/deploy — log and continue.
		if wl, werr := s.appWorkload(ctx, app, orgSlug, projSlug); werr == nil {
			if _, rerr := s.recordRelease(ctx, app, wl, "", domain.ReleaseSuperseded); rerr != nil {
				s.logRelease(app.ID, rerr)
			}
		} else {
			s.logRelease(app.ID, werr)
		}
	case app.GitRepository != "":
		// Git-source app: create a Build record, persist the app as "building", and
		// kick off the build asynchronously. The build (on success) sets the app's
		// image and deploys it through the normal Apply path.
		app.Status = "building"
		if err := s.store.CreateApp(ctx, app); err != nil {
			return nil, err
		}
		if err := s.startBuild(ctx, app, orgSlug, projSlug); err != nil {
			return nil, err
		}
	default:
		// Neither an image nor a git repo: nothing to deploy yet; persist as queued.
		if err := s.store.CreateApp(ctx, app); err != nil {
			return nil, err
		}
	}
	return app, nil
}

// imageRef computes the target push image ref for an app from UNAMBIGUOUS,
// slash-delimited tenant IDs: <registry>/<orgID>/<projectID>/<appID>:<tag>. Using
// the (already-unique, opaque) IDs joined by '/' — rather than concatenating
// sanitized slugs with a separator — avoids cross-tenant path collisions (two
// distinct tenants whose sanitized slugs collapse to the same string) and makes
// the path non-guessable. The registry prefix is admin/DB-driven
// (VORTEX_BUILD_REGISTRY); when unset a bare "<orgID>/<projectID>/<appID>:<tag>"
// ref is produced so the FakeBuilder flow still works end-to-end in dev/tests.
func (s *Service) imageRef(orgID, projectID, appID, tag string) string {
	proj := kubeSanitize(projectID)
	if proj == "" {
		proj = "default"
	}
	repo := strings.Join([]string{kubeSanitize(orgID), proj, kubeSanitize(appID)}, "/")
	if s.buildRegistry != "" {
		repo = strings.TrimRight(s.buildRegistry, "/") + "/" + repo
	}
	if tag == "" {
		tag = "latest"
	}
	return repo + ":" + tag
}

// startBuild records a "building" Build, computes the target image ref, and runs
// the build asynchronously (detached, timeout-bound, WaitGroup-tracked). On
// success it sets the app image and deploys; on failure it marks the app
// "build_failed" and the Build "failed" with logs.
func (s *Service) startBuild(ctx context.Context, app *domain.App, orgSlug, projSlug string) error {
	bld := &domain.Build{
		ID:        s.idgen(),
		AppID:     app.ID,
		OrgID:     app.OrgID,
		Status:    domain.BuildBuilding,
		CommitRef: app.GitBranch,
		CreatedAt: s.now(),
	}
	if err := s.store.CreateBuild(ctx, bld); err != nil {
		return err
	}

	// The image TAG includes the BUILD id so every build pushes a DISTINCT image
	// ref the deploy then references — otherwise a rebuild would reuse a tag and the
	// deploy could roll out a stale (cached) image.
	tag := buildRef(app.GitBranch, bld.ID)
	req := build.Request{
		AppID:       app.ID,
		BuildID:     bld.ID,
		OrgSlug:     orgSlug,
		ProjectSlug: projSlug,
		AppName:     app.Name,
		GitRepo:     app.GitRepository,
		GitRef:      app.GitBranch,
		Dockerfile:  "Dockerfile",
		ImageRef:    s.imageRef(app.OrgID, app.ProjectID, app.ID, tag),
		// SECURITY: runtime env/secrets are NOT forwarded as kaniko --build-args
		// (they would be baked into image layers and exposed in the Job spec).
		// Runtime env reaches pods only via the deploy Workload.Env path.
		BuildArgs: nil,
	}

	s.runBuildAsync(app.ID, bld.ID, orgSlug, projSlug, req)
	return nil
}

// runBuildAsync executes one build on a detached, timeout-bound context in a
// goroutine registered with the shutdown WaitGroup. It is panic-guarded so a bad
// build can never crash the process.
func (s *Service) runBuildAsync(appID, buildID, orgSlug, projSlug string, req build.Request) {
	if s.buildWG != nil {
		s.buildWG.Add(1)
	}
	go func() {
		if s.buildWG != nil {
			defer s.buildWG.Done()
		}
		defer func() {
			if r := recover(); r != nil {
				s.finishBuildFailure(context.Background(), appID, buildID, fmt.Sprintf("build panic: %v", r))
			}
		}()

		ctx, cancel := context.WithTimeout(context.Background(), s.buildTimeout)
		defer cancel()

		res, err := s.builder.Build(ctx, req)
		if err != nil {
			s.finishBuildFailure(ctx, appID, buildID, err.Error())
			return
		}
		s.finishBuildSuccess(ctx, appID, buildID, orgSlug, projSlug, res.Image)
	}()
}

// finishBuildSuccess records the produced image, marks the Build succeeded, then
// deploys the app through the normal Apply path (release/host/env/domains).
//
// CONCURRENCY: the async worker re-fetches the app immediately before every
// UpdateApp and only writes the worker-owned fields (Image/Release/Host/Status),
// so it never clobbers a concurrent user edit (env/domains/size). If the user
// changed intent mid-build to "stopped", the freshly-built image is recorded but
// the app is NOT resurrected/deployed — the build is marked done and the app stays
// stopped.
//
// REVENUE GATING: CreateApp/Deploy gate at REQUEST time, but a git build runs
// asynchronously and can finish minutes later — after the subscription was canceled
// or the spend cap was hit, or while the workload is oversized for the org's current
// plan. So before deploying the built image we re-run EnsureActive + the runtime
// workload-size check. On ErrPaymentRequired / ErrQuotaExceeded we record the built
// image (so a later Start/Deploy can ship it once resolved) but leave the app
// UN-deployed with status "blocked" — we never Apply paid work the gate would deny.
func (s *Service) finishBuildSuccess(ctx context.Context, appID, buildID, orgSlug, projSlug, image string) {
	// Mark the Build succeeded with the produced image (build-owned record).
	if b, err := s.store.GetBuild(ctx, buildID); err == nil {
		b.Status = domain.BuildSucceeded
		b.Image = image
		b.FinishedAt = s.now()
		_ = s.store.UpdateBuild(ctx, b)
	}

	// Re-fetch before recording the image so a concurrent user edit is preserved.
	app, err := s.store.GetApp(ctx, appID)
	if err != nil {
		return
	}
	// User stopped the app mid-build: do NOT resurrect/deploy. Record the image so a
	// later Start/Deploy can ship it, but leave the stopped status untouched.
	if app.Status == "stopped" {
		app.Image = image
		_ = s.store.UpdateApp(ctx, app)
		return
	}
	app.Image = image
	if err := s.store.UpdateApp(ctx, app); err != nil {
		return
	}

	// Re-gate at deploy time: the subscription/spend-cap or plan size may have changed
	// since the build was requested. If the gate now denies the deploy, record the
	// image (done above) but leave the app "blocked" and DO NOT Apply.
	if gateErr := s.gateBuiltDeploy(ctx, app); gateErr != nil {
		if app2, err := s.store.GetApp(ctx, appID); err == nil && app2.Status != "stopped" {
			app2.Status = "blocked"
			_ = s.store.UpdateApp(ctx, app2)
		}
		return
	}

	// Deploy the freshly-built image (ensuring the tenant pull secret first). A
	// failure here marks the app "build_failed".
	release, host, derr := s.deployBuiltApp(ctx, app, orgSlug, projSlug)

	// Re-fetch again before the post-deploy write so we don't clobber an edit (or a
	// user Stop) that landed during the deploy.
	app, err = s.store.GetApp(ctx, appID)
	if err != nil {
		return
	}
	if app.Status == "stopped" {
		// User stopped during the deploy: leave stopped, don't flip to deploying.
		return
	}
	if derr != nil {
		app.Status = "build_failed"
		_ = s.store.UpdateApp(ctx, app)
		return
	}
	app.Release = release
	app.Host = host
	app.Status = "deploying"
	_ = s.store.UpdateApp(ctx, app)
	// Record the release for this built deploy. Best-effort (the deploy succeeded).
	if wl, werr := s.appWorkload(ctx, app, orgSlug, projSlug); werr == nil {
		if _, rerr := s.recordRelease(ctx, app, wl, "", domain.ReleaseSuperseded); rerr != nil {
			s.logRelease(app.ID, rerr)
		}
	} else {
		s.logRelease(app.ID, werr)
	}
}

// gateBuiltDeploy re-applies the request-time revenue/quota gates before an
// async build's image is deployed: the subscription/spend-cap (EnsureActive) and
// the workload size against the org's CURRENT plan. It returns the gating error
// (ErrPaymentRequired / ErrQuotaExceeded) when the deploy must be blocked, or nil
// when it may proceed.
func (s *Service) gateBuiltDeploy(ctx context.Context, app *domain.App) error {
	if err := s.ensureActive(ctx, app.OrgID); err != nil {
		return err
	}
	return s.checkWorkloadSize(ctx, app.OrgID, app.CPU, app.MemoryMB)
}

// finishBuildFailure marks the app "build_failed" and the Build "failed" with the
// captured logs. It re-fetches the app and only sets the status, and never
// resurrects an app the user stopped mid-build.
func (s *Service) finishBuildFailure(ctx context.Context, appID, buildID, logs string) {
	if app, err := s.store.GetApp(ctx, appID); err == nil {
		if app.Status != "stopped" {
			app.Status = "build_failed"
			_ = s.store.UpdateApp(ctx, app)
		}
	}
	if b, err := s.store.GetBuild(ctx, buildID); err == nil {
		b.Status = domain.BuildFailed
		b.Logs = logs
		b.FinishedAt = s.now()
		_ = s.store.UpdateBuild(ctx, b)
	}
}

// ListBuilds returns a bounded page of the org's builds for one of its apps
// (newest first). The Page bounds the read so a busy app's build history can
// never be returned unbounded.
func (s *Service) ListBuilds(ctx context.Context, orgID, appID string, p store.Page) ([]domain.Build, error) {
	if _, err := s.ownedApp(ctx, orgID, appID); err != nil {
		return nil, err
	}
	return s.store.ListBuildsByApp(ctx, appID, p)
}

// GetBuild returns one build (incl. logs), ensuring it belongs to the org's app.
func (s *Service) GetBuild(ctx context.Context, orgID, appID, buildID string) (*domain.Build, error) {
	if _, err := s.ownedApp(ctx, orgID, appID); err != nil {
		return nil, err
	}
	b, err := s.store.GetBuild(ctx, buildID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if b.AppID != appID || b.OrgID != orgID {
		return nil, ErrNotFound
	}
	return b, nil
}

// buildRef derives a short, image-tag-safe tag for a build from the git ref and
// the BUILD id, so every build produces a DISTINCT tag (e.g. "main-1a2b3c4d") and
// a rebuild can never reuse a prior tag / roll out a stale cached image.
func buildRef(gitRef, buildID string) string {
	ref := imageTagSanitize(gitRef)
	if ref == "" {
		ref = "build"
	}
	short := imageTagSanitize(buildID)
	if len(short) > 8 {
		short = short[:8]
	}
	if short == "" {
		return ref
	}
	return ref + "-" + short
}

// imageTagSafe keeps a string valid as a Docker image tag: alphanumerics, '.',
// '_' and '-', collapsed and trimmed of leading separators.
var imageTagSafe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func imageTagSanitize(s string) string {
	s = strings.TrimSpace(s)
	s = imageTagSafe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-._")
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}

// kubeSanitize lowercases and collapses a slug to the same DNS-safe charset the
// kube package uses for namespaces/releases, so a computed image repo path is
// always valid.
var kubeNonDNS = regexp.MustCompile(`[^a-z0-9-]+`)

func kubeSanitize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = kubeNonDNS.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	return s
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

// AppLogStream streams an org's app logs to w via the backend (tenant-scoped: the
// app must belong to the org). With follow it blocks until ctx is cancelled
// (client disconnect) or the stream ends. An app with no Release yet is a no-op
// (returns nil) so the caller can close the stream cleanly. The caller is
// responsible for flushing w per line (e.g. SSE).
func (s *Service) AppLogStream(ctx context.Context, orgID, appID string, opts kube.LogStreamOptions, w io.Writer) error {
	app, err := s.ownedApp(ctx, orgID, appID)
	if err != nil {
		return err
	}
	if app.Release == "" {
		return nil
	}
	return s.backend.LogStream(ctx, app.Namespace, app.Release, opts, w)
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

// Deploy (re)deploys an app: it re-renders the full kube.Workload (image, env,
// custom domains, requested size) and runs a helm upgrade via backend.Apply, so
// env/domain/image changes actually reach the pods.
//
// A git-source app with no built image yet RE-TRIGGERS a build (records a new
// Build, marks the app "building", and runs the builder asynchronously) rather
// than faking a deploy. A non-git app with no image still returns ErrNoImage.
func (s *Service) Deploy(ctx context.Context, orgID, appID string) (*domain.App, error) {
	app, err := s.ownedApp(ctx, orgID, appID)
	if err != nil {
		return nil, err
	}
	// Revenue protection + runtime quota: a (re)deploy is new paid work, so re-check
	// the subscription/spend-cap and re-validate the workload size against the org's
	// CURRENT plan (a downgraded oversized workload can't be redeployed).
	if err := s.ensureActive(ctx, orgID); err != nil {
		return nil, err
	}
	if err := s.checkWorkloadSize(ctx, orgID, app.CPU, app.MemoryMB); err != nil {
		return nil, err
	}
	orgSlug := s.orgSlug(ctx, orgID)
	projSlug := s.projectSlug(ctx, app.ProjectID)

	if strings.TrimSpace(app.Image) == "" {
		// Git apps rebuild on deploy; image-less non-git apps have nothing to ship.
		if strings.TrimSpace(app.GitRepository) == "" {
			return nil, ErrNoImage
		}
		app.Status = "building"
		if err := s.store.UpdateApp(ctx, app); err != nil {
			return nil, err
		}
		if err := s.startBuild(ctx, app, orgSlug, projSlug); err != nil {
			return nil, err
		}
		return app, nil
	}

	release, host, err := s.applyApp(ctx, app, orgSlug, projSlug)
	if err != nil {
		return nil, err
	}
	app.Release = release
	app.Host = host
	app.Status = "deploying"
	if err := s.store.UpdateApp(ctx, app); err != nil {
		return nil, err
	}
	// Record a new release revision for this (re)deploy. Best-effort.
	if wl, werr := s.appWorkload(ctx, app, orgSlug, projSlug); werr == nil {
		if _, rerr := s.recordRelease(ctx, app, wl, "", domain.ReleaseSuperseded); rerr != nil {
			s.logRelease(app.ID, rerr)
		}
	} else {
		s.logRelease(app.ID, werr)
	}
	return app, nil
}

// Stop scales the app to zero on the backend.
func (s *Service) Stop(ctx context.Context, orgID, appID string) (*domain.App, error) {
	return s.action(ctx, orgID, appID, "stopped", s.backend.Stop)
}

// Restart triggers a rollout restart of the app on the backend. Like Deploy it
// re-checks the subscription/spend-cap and re-validates the workload size against
// the org's current plan, so a downgraded oversized workload can't be restarted.
func (s *Service) Restart(ctx context.Context, orgID, appID string) (*domain.App, error) {
	app, err := s.ownedApp(ctx, orgID, appID)
	if err != nil {
		return nil, err
	}
	if err := s.ensureActive(ctx, orgID); err != nil {
		return nil, err
	}
	if err := s.checkWorkloadSize(ctx, orgID, app.CPU, app.MemoryMB); err != nil {
		return nil, err
	}
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
	StorageGB int // persistent-volume size; <=0 falls back to the platform default
}

// dbStorage resolves the persistent-volume size (GiB) for a managed database,
// preferring the explicit request, then the admin-configured default, then the
// built-in minimum. It never returns 0 so a database is always durable.
func (s *Service) dbStorage(req int) int {
	if req > 0 {
		return req
	}
	if s.dbStorageGB > 0 {
		return s.dbStorageGB
	}
	return defaultDBStorageGB
}

// dbWorkload renders the full kube.Workload for a stored database from its engine
// template (image/port/kind), resolved resources, persistent-volume size, and
// the engine-appropriate credential ENV (so a redeploy re-applies identical
// chart values via helm upgrade). The stored credentials make the env injection
// deterministic across deploys.
func (s *Service) dbWorkload(ctx context.Context, db *domain.Database, orgSlug, projSlug string) (kube.Workload, error) {
	tmpl, ok, err := s.templateByKey(ctx, db.Engine)
	if err != nil {
		return kube.Workload{}, err
	}
	if !ok || catalog.Kind(tmpl.Kind) != catalog.KindDatabase {
		return kube.Workload{}, fmt.Errorf("%w: %q", ErrInvalidTemplate, db.Engine)
	}
	cpuF, memF := s.overcommitFactors(ctx)
	// Clamp the volume size to the platform minimum/default on the guaranteed
	// render path: a 0/legacy StorageGB must NEVER reach the chart, or it would
	// render a volume-less StatefulSet with Delete retention (any restart/Stop
	// would wipe the data). dbStorage never returns 0.
	storageGB := s.dbStorage(db.StorageGB)
	return kube.Workload{
		OrgSlug:                orgSlug,
		ProjectSlug:            projSlug,
		Name:                   db.Name,
		Kind:                   "database",
		Image:                  tmpl.Image,
		Port:                   tmpl.DefaultPort,
		CPU:                    db.CPU,
		MemoryMB:               db.MemoryMB,
		StorageGB:              storageGB,
		StorageClass:           s.dbStorageClass,
		Env:                    kube.DatabaseEnv(tmpl.Key, db.DatabaseName, db.Username, db.Password),
		ServiceTemplateKey:     tmpl.Key,
		CPUOvercommitFactor:    cpuF,
		MemoryOvercommitFactor: memF,
	}, nil
}

// CreateDatabase provisions a managed database for the org: it resolves the
// engine to a catalog template (image/port, DB/admin-driven), enforces the
// plan quota, ensures the tenant namespace, and deploys the engine as a
// StatefulSet via the backend. The Kubernetes placement is persisted and the
// database is marked "deploying".
func (s *Service) CreateDatabase(ctx context.Context, orgID string, in CreateDatabaseInput) (*domain.Database, error) {
	if err := s.ensureActive(ctx, orgID); err != nil {
		return nil, err
	}
	engine := strings.ToLower(strings.TrimSpace(in.Engine))
	if engine == "" {
		engine = "postgresql"
	}
	tmpl, ok, err := s.templateByKey(ctx, engine)
	if err != nil {
		return nil, err
	}
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

	// Generate a strong random password + SQL-safe db/user so the engine container
	// initializes itself with real credentials and the connection-info endpoint can
	// return them. (Stored plaintext-at-rest for now — see Database doc TODO.)
	dbName, user, password, err := generateDBCredentials(name)
	if err != nil {
		return nil, fmt.Errorf("platform: generate db credentials: %w", err)
	}

	db := &domain.Database{
		ID:           s.idgen(),
		OrgID:        orgID,
		ProjectID:    in.ProjectID,
		Name:         name,
		Engine:       engine,
		CPU:          cpu,
		MemoryMB:     memMB,
		StorageGB:    s.dbStorage(in.StorageGB),
		Username:     user,
		Password:     password,
		DatabaseName: dbName,
		Status:       "deploying",
		Namespace:    namespace,
		CreatedAt:    s.now(),
	}

	w, err := s.dbWorkload(ctx, db, orgSlug, projSlug)
	if err != nil {
		return nil, err
	}
	release, host, err := s.backend.Apply(ctx, w)
	if err != nil {
		return nil, err
	}
	db.Release = release
	db.Host = host

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

// DatabaseConnInfo is the connection detail for a managed database. Databases
// are internal-only (ClusterIP, no public gateway), so Host is the in-cluster
// service DNS — reachable from the org's own workloads, not the public internet.
type DatabaseConnInfo struct {
	Host             string `json:"host"`     // <release>.<namespace>.svc.cluster.local
	Port             int    `json:"port"`     // engine default port
	Database         string `json:"database"` // initialized database name
	Username         string `json:"username"`
	Password         string `json:"password"`
	ConnectionString string `json:"connectionString"`
}

// DatabaseDetail bundles a database record with its (internal) connection info,
// returned by the connection-info endpoint.
type DatabaseDetail struct {
	*domain.Database
	Connection DatabaseConnInfo `json:"connection"`
}

// GetDatabaseDetail returns one database scoped to the org plus its in-cluster
// connection info (host/port/credentials/connectionString). Cross-tenant access
// is hidden as ErrNotFound by ownedDatabase.
func (s *Service) GetDatabaseDetail(ctx context.Context, orgID, dbID string) (*DatabaseDetail, error) {
	db, err := s.ownedDatabase(ctx, orgID, dbID)
	if err != nil {
		return nil, err
	}
	return &DatabaseDetail{Database: db, Connection: s.databaseConnInfo(db)}, nil
}

// databaseConnInfo derives the in-cluster connection info for a database from
// its placement (release/namespace) and stored credentials. The host is the
// stable Kubernetes service DNS so the value survives pod restarts/rescheduling.
func (s *Service) databaseConnInfo(db *domain.Database) DatabaseConnInfo {
	host := db.Host
	if db.Release != "" && db.Namespace != "" {
		host = db.Release + "." + db.Namespace + ".svc.cluster.local"
	}
	port := enginePort(db.Engine)
	ci := DatabaseConnInfo{
		Host:     host,
		Port:     port,
		Database: db.DatabaseName,
		Username: db.Username,
		Password: db.Password,
	}
	ci.ConnectionString = connectionString(db.Engine, ci)
	return ci
}

// enginePort returns the default service port for a database engine.
func enginePort(engine string) int {
	switch strings.ToLower(strings.TrimSpace(engine)) {
	case "redis":
		return 6379
	case "postgresql", "postgres":
		return 5432
	case "mysql", "mariadb":
		return 3306
	case "mongodb", "mongo":
		return 27017
	default:
		return 5432
	}
}

// connectionString renders a ready-to-use, engine-appropriate URI from the
// connection info. Credentials are URL-escaped so a special character in the
// generated password can't break the URI.
func connectionString(engine string, ci DatabaseConnInfo) string {
	u, p := url.QueryEscape(ci.Username), url.QueryEscape(ci.Password)
	hostPort := fmt.Sprintf("%s:%d", ci.Host, ci.Port)
	switch strings.ToLower(strings.TrimSpace(engine)) {
	case "redis":
		// redis AUTH uses the password only (no username on the default ACL user).
		return fmt.Sprintf("redis://:%s@%s", p, hostPort)
	case "mysql", "mariadb":
		return fmt.Sprintf("mysql://%s:%s@%s/%s", u, p, hostPort, ci.Database)
	case "mongodb", "mongo":
		// The root user is created in the `admin` db (MONGO_INITDB_ROOT_*), so
		// authentication must target it via authSource=admin or auth fails.
		return fmt.Sprintf("mongodb://%s:%s@%s/%s?authSource=admin", u, p, hostPort, ci.Database)
	default: // postgresql
		return fmt.Sprintf("postgres://%s:%s@%s/%s", u, p, hostPort, ci.Database)
	}
}

// DeployDatabase (re)deploys a managed database: it re-renders the workload from
// its engine template + stored credentials/resources/storage and runs a helm
// upgrade via backend.Apply, so a stopped database is brought back up with
// identical wiring (and its retained PVC reattached).
func (s *Service) DeployDatabase(ctx context.Context, orgID, dbID string) (*domain.Database, error) {
	db, err := s.ownedDatabase(ctx, orgID, dbID)
	if err != nil {
		return nil, err
	}
	if err := s.ensureActive(ctx, orgID); err != nil {
		return nil, err
	}
	if err := s.checkWorkloadSize(ctx, orgID, db.CPU, db.MemoryMB); err != nil {
		return nil, err
	}
	orgSlug := s.orgSlug(ctx, orgID)
	projSlug := s.projectSlug(ctx, db.ProjectID)

	w, err := s.dbWorkload(ctx, db, orgSlug, projSlug)
	if err != nil {
		return nil, err
	}
	release, host, err := s.backend.Apply(ctx, w)
	if err != nil {
		return nil, err
	}
	db.Release = release
	db.Host = host
	db.Status = "deploying"
	if err := s.store.UpdateDatabase(ctx, db); err != nil {
		return nil, err
	}
	return db, nil
}

// StopDatabase scales the database to zero on the backend (its retained PVC is
// kept so data survives).
func (s *Service) StopDatabase(ctx context.Context, orgID, dbID string) (*domain.Database, error) {
	return s.databaseAction(ctx, orgID, dbID, "stopped", s.backend.Stop)
}

// StartDatabase scales a stopped database back up on the backend, reattaching
// its retained volume. It re-checks subscription/spend-cap and plan size, so a
// canceled or downgraded org can't bring paid workloads back online.
func (s *Service) StartDatabase(ctx context.Context, orgID, dbID string) (*domain.Database, error) {
	if err := s.ensureDatabaseRunnable(ctx, orgID, dbID); err != nil {
		return nil, err
	}
	return s.databaseAction(ctx, orgID, dbID, "running", s.backend.Start)
}

// RestartDatabase triggers a rollout restart of the database on the backend, after
// re-checking subscription/spend-cap and plan size.
func (s *Service) RestartDatabase(ctx context.Context, orgID, dbID string) (*domain.Database, error) {
	if err := s.ensureDatabaseRunnable(ctx, orgID, dbID); err != nil {
		return nil, err
	}
	return s.databaseAction(ctx, orgID, dbID, "restarting", s.backend.Restart)
}

// ensureDatabaseRunnable re-checks the org's subscription/spend-cap and the
// database's size against the org's current plan before reviving/restarting it.
func (s *Service) ensureDatabaseRunnable(ctx context.Context, orgID, dbID string) error {
	db, err := s.ownedDatabase(ctx, orgID, dbID)
	if err != nil {
		return err
	}
	if err := s.ensureActive(ctx, orgID); err != nil {
		return err
	}
	return s.checkWorkloadSize(ctx, orgID, db.CPU, db.MemoryMB)
}

// databaseAction applies a status transition, invoking the backend lifecycle
// call for the database's release when it has been deployed (release is kept).
func (s *Service) databaseAction(ctx context.Context, orgID, dbID, status string,
	fn func(context.Context, string, string) error) (*domain.Database, error) {
	db, err := s.ownedDatabase(ctx, orgID, dbID)
	if err != nil {
		return nil, err
	}
	if db.Release != "" {
		if err := fn(ctx, db.Namespace, db.Release); err != nil {
			return nil, err
		}
	}
	db.Status = status
	if err := s.store.UpdateDatabase(ctx, db); err != nil {
		return nil, err
	}
	return db, nil
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
