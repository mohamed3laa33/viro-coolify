// Package httpx wires the Viro control-plane HTTP API: router, middleware and handlers.
package httpx

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/auth"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/billing"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/build"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/config"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/identity"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/kube"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/platform"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/store"
)

// Server holds the API dependencies and the composed router.
type Server struct {
	cfg      *config.Config
	logger   *slog.Logger
	backend  kube.Backend
	builder  build.Builder
	store    store.Store
	tokens   *auth.TokenManager
	identity *identity.Service
	platform *platform.Service
	billing  *billing.Service
	router   chi.Router

	// buildWG tracks in-flight async git builds so a graceful shutdown can wait
	// for them to drain (see WaitBuilds). It is passed to the platform service.
	buildWG sync.WaitGroup
}

// Option customizes Server construction (used by tests to inject a deploy
// backend without touching a real cluster).
type Option func(*serverOptions)

type serverOptions struct {
	backend kube.Backend
	builder build.Builder
}

// WithBackend overrides the Kubernetes deploy backend (e.g. kube.FakeBackend in
// tests). When unset, NewServer builds the backend from config.
func WithBackend(b kube.Backend) Option {
	return func(o *serverOptions) { o.backend = b }
}

// WithBuilder overrides the git-source image builder (e.g. build.FakeBuilder in
// tests). When unset, NewServer builds the builder from cluster config (with a
// FakeBuilder fallback when no cluster is reachable).
func WithBuilder(b build.Builder) Option {
	return func(o *serverOptions) { o.builder = b }
}

// NewServer constructs a Server with its dependencies and routes wired up.
//
// The control-plane store defaults to an in-memory implementation (great for
// local development and tests); a Postgres store satisfies the same interface
// and is swapped in by configuration.
func NewServer(cfg *config.Config, logger *slog.Logger, st store.Store, opts ...Option) *Server {
	var so serverOptions
	for _, opt := range opts {
		opt(&so)
	}
	tokens := auth.NewTokenManager(
		cfg.JWTSecret,
		time.Duration(cfg.JWTAccessTTL)*time.Minute,
		time.Duration(cfg.JWTRefreshTTL)*time.Hour,
	)
	// Kubernetes deploy backend. Build the real KubeBackend from in-cluster
	// config / kubeconfig; on failure (e.g. local dev with no cluster) fall back
	// to the in-memory FakeBackend so the API still boots. The fake is a real
	// test double — NOT a demo success path that pretends deploys happened.
	backend := so.backend
	if backend == nil {
		backend = newKubeBackend(cfg, logger)
	}

	// Git-source image builder: build the real KanikoBuilder from cluster config;
	// on failure (local dev with no cluster) fall back to the in-memory
	// FakeBuilder so the API still boots and the git→build→deploy flow is
	// exercised end-to-end (NOT a demo success path — see build.FakeBuilder).
	builder := so.builder
	if builder == nil {
		builder = newBuilder(cfg, logger)
	}

	// Payment provider: Stripe when billing is enabled and configured, else a mock
	// that activates subscriptions locally (so the billing UX works in dev).
	var provider billing.PaymentProvider = billing.MockProvider{}
	if cfg.BillingEnabled && cfg.StripeSecretKey != "" {
		webBase := "http://localhost:3000"
		if len(cfg.CORSAllowedOrigins) > 0 {
			webBase = cfg.CORSAllowedOrigins[0]
		}
		provider = billing.NewStripeProvider(
			cfg.StripeSecretKey,
			webBase+"/dashboard/settings?billing=success",
			webBase+"/dashboard/settings?billing=cancel",
		)
	}

	bill := billing.NewService(st, provider)
	s := &Server{
		cfg:      cfg,
		logger:   logger,
		backend:  backend,
		builder:  builder,
		store:    st,
		tokens:   tokens,
		identity: identity.NewService(st, tokens, cfg.AdminEmails),
		billing:  bill,
	}
	// Build the platform service with the git image builder wired in, threading
	// the server's build WaitGroup so in-flight builds drain on shutdown. The
	// per-tenant imagePullSecret is only attached/ensured when a registry pull
	// secret SOURCE is configured (production); local/dev leaves it empty so the
	// FakeBuilder/public-image flows don't reference a non-existent secret.
	pullSecretName := ""
	if cfg.RegistryPullSecretSource != "" {
		pullSecretName = cfg.RegistryPullSecret
	}
	s.platform = platform.NewService(st, backend, bill,
		platform.WithBuilder(builder),
		platform.WithBuildRegistry(cfg.BuildRegistry),
		platform.WithPullSecretName(pullSecretName),
		platform.WithBuildTimeout(time.Duration(cfg.BuildTimeoutSec)*time.Second),
		platform.WithBuildWaitGroup(&s.buildWG),
		platform.WithDBStorageDefault(cfg.DBDefaultStorageGB),
		platform.WithDBStorageClass(cfg.DBStorageClass),
	)
	s.router = s.routes()
	return s
}

// WaitBuilds blocks until all in-flight async git builds have finished. Call it
// on graceful shutdown (after the HTTP server has drained) before closing the
// store, so a mid-flight build never writes to a pool that is about to close.
func (s *Server) WaitBuilds() { s.buildWG.Wait() }

// newBuilder builds the git-source image builder from config. When the cluster
// is unreachable it logs a warning and returns an in-memory FakeBuilder so
// local/dev still boots.
func newBuilder(cfg *config.Config, logger *slog.Logger) build.Builder {
	cs, err := kube.NewClientset(cfg.Kubeconfig)
	if err != nil {
		logger.Warn("build: cluster unavailable; falling back to in-memory FakeBuilder",
			"err", err)
		return build.NewFakeBuilder()
	}
	// SECURITY (item 4): only mount the shared push secret when a registry is
	// actually configured. With no registry (local/dev) nothing is mounted, so a
	// tenant build pod can never read registry credentials. See build.Config.PushSecret
	// for the deferred per-org-scoped-token TODO.
	pushSecret := ""
	if cfg.BuildRegistry != "" {
		pushSecret = cfg.BuildPushSecret
	}
	return build.NewKanikoBuilder(build.Config{
		Namespace:            cfg.BuildNamespace,
		KanikoImage:          cfg.BuildKanikoImage,
		PushSecret:           pushSecret,
		GitCredentialsSecret: cfg.BuildGitCreds,
		Timeout:              time.Duration(cfg.BuildTimeoutSec) * time.Second,
	}, cs)
}

// newKubeBackend builds the Kubernetes deploy backend from config. When the
// cluster is unreachable (no in-cluster config and no usable kubeconfig), it
// logs a warning and returns an in-memory FakeBackend so local/dev still boots.
func newKubeBackend(cfg *config.Config, logger *slog.Logger) kube.Backend {
	// Overcommit factors are admin/DB-configurable platform settings; seed the
	// backend with the seeded defaults here. Per-call sites pass the live quota
	// derived from the org's plan.
	settings := store.DefaultSettings()
	kc := kube.Config{
		BaseDomain:                  cfg.BaseDomain,
		ChartPath:                   cfg.KubeChartPath,
		GatewayName:                 cfg.GatewayName,
		GatewayNamespace:            cfg.GatewayNamespace,
		CPUOvercommitFactor:         settings.CPUOvercommitFactor,
		MemoryOvercommitFactor:      settings.MemoryOvercommitFactor,
		HelmTimeout:                 time.Duration(cfg.HelmTimeoutSec) * time.Second,
		RegistryPullSecret:          cfg.RegistryPullSecretSource,
		RegistryPullSecretNamespace: cfg.RegistryPullSecretNamespace,
	}
	be, err := kube.New(kc, cfg.Kubeconfig, nil)
	if err != nil {
		logger.Warn("kube backend unavailable; falling back to in-memory FakeBackend",
			"err", err)
		fb := kube.NewFakeBackend()
		fb.BaseDomain = cfg.BaseDomain
		return fb
	}
	return be
}

// Router returns the composed HTTP handler.
func (s *Server) Router() http.Handler { return s.router }

// StartMetering launches a background ticker that records one interval of compute
// cost for every org at the live admin price list (hourly pricing). It returns
// immediately and stops when ctx is cancelled. interval<=0 defaults to one hour.
// When wg is non-nil it is incremented before the loop starts and Done()'d when it
// exits, so the caller can Wait() for the loop to drain (e.g. before closing the
// store) and avoid querying a pool that is about to close.
func (s *Server) StartMetering(ctx context.Context, interval time.Duration, wg *sync.WaitGroup) {
	if interval <= 0 {
		interval = time.Hour
	}
	if wg != nil {
		wg.Add(1)
	}
	go func() {
		if wg != nil {
			defer wg.Done()
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.meterOnce(ctx)
			}
		}
	}()
}

// meterOnce runs a single metering pass, recovering from panics so a single bad
// tick can never crash the process or kill the metering goroutine.
func (s *Server) meterOnce(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("metering panic recovered", "panic", r)
		}
	}()
	n, err := s.billing.MeterUsage(ctx)
	if err != nil {
		// MeterUsage is continue-on-error; err is the first per-org failure.
		s.logger.Error("metering tick", "err", err)
	}
	s.logger.Info("metered usage", "orgs", n)
}

// StartReconciler launches a background ticker that reconciles the stored status
// of every workload (app, service, database) that has a Release against the live
// backend status, writing back a real phase (running/pending/failed/stopped/
// scaled-to-zero). It returns immediately and stops when ctx is cancelled.
// interval<=0 defaults to 30s. When wg is non-nil it is incremented before the
// loop starts and Done()'d when it exits, so the caller can Wait() for the loop to
// drain (e.g. before closing the store) and avoid querying a pool that is about to
// close.
func (s *Server) StartReconciler(ctx context.Context, interval time.Duration, wg *sync.WaitGroup) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	if wg != nil {
		wg.Add(1)
	}
	go func() {
		if wg != nil {
			defer wg.Done()
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.reconcileOnce(ctx)
			}
		}
	}()
}

// reconcileOnce performs one reconciliation pass over all orgs' workloads. The
// pass itself is panic-guarded; each individual workload is additionally isolated
// (see reconcileWorkload) so one bad workload never aborts the rest of the tick.
func (s *Server) reconcileOnce(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("reconciler panic recovered", "panic", r)
		}
	}()

	orgs, err := s.store.ListAllOrgs(ctx)
	if err != nil {
		s.logger.Error("reconcile: list orgs", "err", err)
		return
	}
	for _, org := range orgs {
		// Short-circuit on shutdown so we don't keep querying the pool after the
		// HTTP server has drained and the store is about to close.
		if ctx.Err() != nil {
			return
		}
		s.reconcileOrg(ctx, org.ID)
	}
}

// reconcileOrg reconciles every app/service/database in one org.
func (s *Server) reconcileOrg(ctx context.Context, orgID string) {
	apps, err := s.store.ListAppsByOrg(ctx, orgID)
	if err != nil {
		s.logger.Error("reconcile: list apps", "org", orgID, "err", err)
	}
	for i := range apps {
		a := apps[i]
		s.reconcileWorkload(ctx, a.ID, a.Status, a.Namespace, a.Release, func(status string) error {
			a.Status = status
			return s.store.UpdateApp(ctx, &a)
		})
	}

	svcs, err := s.store.ListServicesByOrg(ctx, orgID)
	if err != nil {
		s.logger.Error("reconcile: list services", "org", orgID, "err", err)
	}
	for i := range svcs {
		sv := svcs[i]
		s.reconcileWorkload(ctx, sv.ID, sv.Status, sv.Namespace, sv.Release, func(status string) error {
			sv.Status = status
			return s.store.UpdateService(ctx, &sv)
		})
	}

	dbs, err := s.store.ListDatabasesByOrg(ctx, orgID)
	if err != nil {
		s.logger.Error("reconcile: list databases", "org", orgID, "err", err)
	}
	for i := range dbs {
		d := dbs[i]
		s.reconcileWorkload(ctx, d.ID, d.Status, d.Namespace, d.Release, func(status string) error {
			d.Status = status
			return s.store.UpdateDatabase(ctx, &d)
		})
	}
}

// reconcileWorkload reconciles a single workload's stored status against the live
// backend, persisting via save when it changes. It is wrapped in its own deferred
// recover() so a panic in one workload (backend call, store write, etc.) is
// isolated and the surrounding pass continues with the next workload.
func (s *Server) reconcileWorkload(ctx context.Context, id, current, namespace, release string, save func(status string) error) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("reconcile: workload panic recovered", "workload", id, "panic", r)
		}
	}()
	if release == "" {
		return
	}
	phase, ok := s.backendPhase(ctx, current, namespace, release)
	if !ok || phase == current {
		return
	}
	if err := save(phase); err != nil {
		s.logger.Error("reconcile: update workload", "workload", id, "err", err)
	}
}

// stickyStatuses are user-initiated / terminal states the reconciler must NEVER
// overwrite. "stopped" gates billing (billable() charges anything that is not
// "stopped"), so clobbering it back to a machine state would resume charges for a
// workload the user explicitly stopped. "build_failed" is the outcome of a failed
// build (incl. a rebuild of a still-running app, whose Deployment is non-empty);
// without stickiness the reconciler would see the old Deployment as "running" and
// silently clobber the failure, hiding it from the user.
var stickyStatuses = map[string]bool{
	"stopped":      true,
	"build_failed": true,
}

// transientIntents are user-initiated states that should be LEFT ALONE until the
// backend resolves them to a terminal machine state (running/failed). They must
// not be flipped to non-terminal machine phases (e.g. "pending",
// "scaled-to-zero") while the intent is still in flight.
var transientIntents = map[string]bool{
	"deploying":  true,
	"restarting": true,
	// A git app being built has no Release yet (reconcileWorkload returns early),
	// but list it explicitly so a build intent is never downgraded if a stale
	// Release lingers on the row.
	"building": true,
}

// terminalMachinePhases are the machine-observable phases that are allowed to
// resolve a transient intent.
var terminalMachinePhases = map[string]bool{
	"running": true,
	"failed":  true,
}

// reconcilePhase computes the status to store for a workload, given its current
// stored status and the live backend status. ok=false means leave the row
// untouched. It respects user intent: sticky statuses are never overwritten, and
// transient intents are only refined once the backend reaches a terminal machine
// state.
func reconcilePhase(current string, st kube.Status) (string, bool) {
	if stickyStatuses[current] {
		return "", false
	}
	phase, ok := mapPhase(st)
	if !ok {
		return "", false
	}
	if transientIntents[current] && !terminalMachinePhases[phase] {
		// Intent still in flight; don't downgrade to a non-terminal machine phase.
		return "", false
	}
	return phase, true
}

// backendPhase reads the live backend status for a release and reconciles it
// against the stored status, honoring user intent (sticky/transient statuses).
// ok=false means the status could not be read or must be left untouched (the row
// is never flipped to a wrong value).
func (s *Server) backendPhase(ctx context.Context, current, namespace, release string) (string, bool) {
	if stickyStatuses[current] {
		// Skip the backend read entirely for sticky statuses.
		return "", false
	}
	st, err := s.backend.Status(ctx, namespace, release)
	if err != nil {
		s.logger.Warn("reconcile: backend status", "ns", namespace, "release", release, "err", err)
		return "", false
	}
	return reconcilePhase(current, st)
}

// mapPhase maps a kube.Status to the stored workload status vocabulary. ok=false
// for unrecognized phases so the caller leaves the stored status untouched rather
// than flipping it to "failed"; only an explicit failed phase reports "failed".
func mapPhase(st kube.Status) (string, bool) {
	switch st.Phase {
	case "Running":
		return "running", true
	case "Scaled to zero":
		return "scaled-to-zero", true
	case "Pending":
		return "pending", true
	case "Failed":
		return "failed", true
	default:
		return "", false
	}
}

func (s *Server) routes() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	// RealIP normalizes RemoteAddr from proxy headers so the per-IP rate limiter
	// keys on the real client. It is only safe behind a trusted proxy that
	// overwrites client-supplied X-Forwarded-For/X-Real-IP; Vortex always runs
	// behind the platform's single shared ingress/LB, which does exactly that, so
	// the spoofing concern in SA1019 does not apply here.
	r.Use(middleware.RealIP) //nolint:staticcheck // SA1019: safe behind trusted ingress (see comment)
	r.Use(requestLogger(s.logger))
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware(s.cfg.CORSAllowedOrigins))
	// CSRF defense-in-depth: reject state-changing browser requests whose
	// Origin/Referer is not in the allowlist. Runs after CORS so OPTIONS
	// preflights are already short-circuited.
	r.Use(csrfOriginGuard(s.cfg.CORSAllowedOrigins))

	r.Get("/healthz", s.handleHealth)
	r.Get("/readyz", s.handleReady)

	// Per-IP rate limiter shared across the public auth + webhook endpoints,
	// which are the most abuse-prone (credential stuffing, webhook floods).
	authLimiter := rateLimit(authRateLimit, authRateWindow)

	r.Route("/v1", func(r chi.Router) {
		r.Get("/version", s.handleVersion)

		// Public auth endpoints (rate-limited per IP).
		r.With(authLimiter).Post("/auth/signup", s.handleSignup)
		r.With(authLimiter).Post("/auth/login", s.handleLogin)
		r.With(authLimiter).Post("/auth/refresh", s.handleRefresh)

		// Public billing: the plan catalog, hourly price list, and the Stripe
		// webhook (signature-verified, rate-limited per IP).
		r.Get("/billing/plans", s.handlePlans)
		r.Get("/billing/pricing", s.handlePricing)
		r.With(authLimiter).Post("/billing/webhook", s.handleStripeWebhook)

		// Public one-click services catalog.
		r.Get("/services/catalog", s.handleServiceCatalog)

		// Authenticated endpoints.
		r.Group(func(r chi.Router) {
			r.Use(s.authMiddleware)

			r.Get("/me", s.handleMe)
			r.Post("/auth/logout", s.handleLogout)

			// Accept an invitation (to an org or a project) as the current user.
			r.Post("/invitations/accept", s.handleAcceptInvitation)

			// Super-admin: DB-backed business config (plans, templates, settings).
			r.Route("/admin", func(r chi.Router) {
				r.Use(s.adminMiddleware)

				r.Get("/overview", s.handleAdminOverview)

				r.Get("/plans", s.handleAdminListPlans)
				r.Post("/plans", s.handleAdminCreatePlan)
				r.Patch("/plans/{id}", s.handleAdminUpdatePlan)
				r.Delete("/plans/{id}", s.handleAdminDeletePlan)

				r.Get("/pricing", s.handleAdminListPricing)
				r.Post("/pricing", s.handleAdminCreatePricing)
				r.Patch("/pricing/{key}", s.handleAdminUpdatePricing)
				r.Delete("/pricing/{key}", s.handleAdminDeletePricing)

				r.Get("/templates", s.handleAdminListTemplates)
				r.Post("/templates", s.handleAdminCreateTemplate)
				r.Patch("/templates/{key}", s.handleAdminUpdateTemplate)
				r.Delete("/templates/{key}", s.handleAdminDeleteTemplate)

				r.Get("/settings", s.handleAdminGetSettings)
				r.Patch("/settings", s.handleAdminUpdateSettings)
			})

			r.Route("/orgs", func(r chi.Router) {
				r.Get("/", s.handleListOrgs)
				r.Post("/", s.handleCreateOrg)

				// Org-scoped resources. Reads require membership (member+);
				// mutations require admin+.
				r.Route("/{orgID}", func(r chi.Router) {
					// Org settings (name, billing email).
					r.With(s.orgAuthz(domain.RoleAdmin)).Patch("/", s.handleUpdateOrg)

					// Members & invitations. Role changes and removals are
					// owner-only; the rest require admin+.
					r.With(s.orgAuthz(domain.RoleMember)).Get("/members", s.handleListMembers)
					r.With(s.orgAuthz(domain.RoleOwner)).Patch("/members/{userID}", s.handleUpdateMember)
					r.With(s.orgAuthz(domain.RoleOwner)).Delete("/members/{userID}", s.handleRemoveMember)
					r.With(s.orgAuthz(domain.RoleAdmin)).Post("/invitations", s.handleCreateInvitation)
					r.With(s.orgAuthz(domain.RoleAdmin)).Get("/invitations", s.handleListInvitations)
					r.With(s.orgAuthz(domain.RoleAdmin)).Delete("/invitations/{inviteID}", s.handleRevokeInvitation)

					// Projects (Org → Project → App).
					r.Route("/projects", func(r chi.Router) {
						r.With(s.orgAuthz(domain.RoleMember)).Get("/", s.handleListProjects)
						r.With(s.orgAuthz(domain.RoleAdmin)).Post("/", s.handleCreateProject)
						r.With(s.orgAuthz(domain.RoleAdmin)).Delete("/{projectID}", s.handleDeleteProject)
						// Project-scoped apps (org admins or project members).
						r.With(s.projectAuthz(domain.RoleMember)).Get("/{projectID}/apps", s.handleListProjectApps)
						r.With(s.projectAuthz(domain.RoleAdmin)).Post("/{projectID}/apps", s.handleCreateAppInProject)
						// Project-scoped one-click services.
						r.With(s.projectAuthz(domain.RoleAdmin)).Post("/{projectID}/services", s.handleCreateService)
					})

					r.Route("/apps", func(r chi.Router) {
						r.With(s.orgAuthz(domain.RoleMember)).Get("/", s.handleListApps)
						r.With(s.orgAuthz(domain.RoleAdmin)).Post("/", s.handleCreateApp)
						r.With(s.orgAuthz(domain.RoleMember)).Get("/{appID}", s.handleGetApp)
						r.With(s.orgAuthz(domain.RoleMember)).Get("/{appID}/logs", s.handleAppLogs)
						r.With(s.orgAuthz(domain.RoleAdmin)).Delete("/{appID}", s.handleDeleteApp)
						r.With(s.orgAuthz(domain.RoleAdmin)).Post("/{appID}/deploy", s.handleDeployApp)
						r.With(s.orgAuthz(domain.RoleAdmin)).Post("/{appID}/stop", s.handleStopApp)
						r.With(s.orgAuthz(domain.RoleAdmin)).Post("/{appID}/restart", s.handleRestartApp)

						// App env / secrets.
						r.With(s.orgAuthz(domain.RoleMember)).Get("/{appID}/env", s.handleListAppEnv)
						r.With(s.orgAuthz(domain.RoleAdmin)).Put("/{appID}/env", s.handleSetAppEnv)
						r.With(s.orgAuthz(domain.RoleAdmin)).Delete("/{appID}/env/{key}", s.handleDeleteAppEnv)

						// App domains.
						r.With(s.orgAuthz(domain.RoleMember)).Get("/{appID}/domains", s.handleListAppDomains)
						r.With(s.orgAuthz(domain.RoleAdmin)).Post("/{appID}/domains", s.handleAddAppDomain)
						r.With(s.orgAuthz(domain.RoleAdmin)).Delete("/{appID}/domains/{domainID}", s.handleDeleteAppDomain)

						// App metrics.
						r.With(s.orgAuthz(domain.RoleMember)).Get("/{appID}/metrics", s.handleAppMetrics)

						// Git-source image builds.
						r.With(s.orgAuthz(domain.RoleMember)).Get("/{appID}/builds", s.handleListBuilds)
						r.With(s.orgAuthz(domain.RoleMember)).Get("/{appID}/builds/{buildID}", s.handleGetBuild)
					})

					r.Route("/services", func(r chi.Router) {
						r.With(s.orgAuthz(domain.RoleMember)).Get("/", s.handleListServices)
						r.With(s.orgAuthz(domain.RoleAdmin)).Post("/{serviceID}/deploy", s.handleDeployService)
						r.With(s.orgAuthz(domain.RoleAdmin)).Post("/{serviceID}/stop", s.handleStopService)
						r.With(s.orgAuthz(domain.RoleAdmin)).Post("/{serviceID}/restart", s.handleRestartService)
						r.With(s.orgAuthz(domain.RoleAdmin)).Delete("/{serviceID}", s.handleDeleteService)
					})

					r.Route("/databases", func(r chi.Router) {
						r.With(s.orgAuthz(domain.RoleMember)).Get("/", s.handleListDatabases)
						r.With(s.orgAuthz(domain.RoleAdmin)).Post("/", s.handleCreateDatabase)
						r.With(s.orgAuthz(domain.RoleMember)).Get("/{databaseID}", s.handleGetDatabase)
						r.With(s.orgAuthz(domain.RoleAdmin)).Post("/{databaseID}/deploy", s.handleDeployDatabase)
						r.With(s.orgAuthz(domain.RoleAdmin)).Post("/{databaseID}/stop", s.handleStopDatabase)
						r.With(s.orgAuthz(domain.RoleAdmin)).Post("/{databaseID}/restart", s.handleRestartDatabase)
						r.With(s.orgAuthz(domain.RoleAdmin)).Delete("/{databaseID}", s.handleDeleteDatabase)
					})

					r.With(s.orgAuthz(domain.RoleMember)).Get("/billing", s.handleGetBilling)
					r.With(s.orgAuthz(domain.RoleAdmin)).Post("/billing/subscribe", s.handleSubscribe)
				})
			})
		})
	})

	return r
}
