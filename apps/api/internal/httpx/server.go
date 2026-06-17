// Package httpx wires the Viro control-plane HTTP API: router, middleware and handlers.
package httpx

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/auth"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/billing"
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
	store    store.Store
	tokens   *auth.TokenManager
	identity *identity.Service
	platform *platform.Service
	billing  *billing.Service
	router   chi.Router
}

// Option customizes Server construction (used by tests to inject a deploy
// backend without touching a real cluster).
type Option func(*serverOptions)

type serverOptions struct {
	backend kube.Backend
}

// WithBackend overrides the Kubernetes deploy backend (e.g. kube.FakeBackend in
// tests). When unset, NewServer builds the backend from config.
func WithBackend(b kube.Backend) Option {
	return func(o *serverOptions) { o.backend = b }
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
		store:    st,
		tokens:   tokens,
		identity: identity.NewService(st, tokens, cfg.AdminEmails),
		platform: platform.NewService(st, backend, bill),
		billing:  bill,
	}
	s.router = s.routes()
	return s
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
		BaseDomain:             cfg.BaseDomain,
		ChartPath:              cfg.KubeChartPath,
		GatewayName:            cfg.GatewayName,
		GatewayNamespace:       cfg.GatewayNamespace,
		CPUOvercommitFactor:    settings.CPUOvercommitFactor,
		MemoryOvercommitFactor: settings.MemoryOvercommitFactor,
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

func (s *Server) routes() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(requestLogger(s.logger))
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware(s.cfg.CORSAllowedOrigins))

	r.Get("/healthz", s.handleHealth)
	r.Get("/readyz", s.handleHealth)

	r.Route("/v1", func(r chi.Router) {
		r.Get("/version", s.handleVersion)

		// Public auth endpoints.
		r.Post("/auth/signup", s.handleSignup)
		r.Post("/auth/login", s.handleLogin)
		r.Post("/auth/refresh", s.handleRefresh)

		// Public billing: the plan catalog and the Stripe webhook (signature-verified).
		r.Get("/billing/plans", s.handlePlans)
		r.Post("/billing/webhook", s.handleStripeWebhook)

		// Public one-click services catalog.
		r.Get("/services/catalog", s.handleServiceCatalog)

		// Authenticated endpoints.
		r.Group(func(r chi.Router) {
			r.Use(s.authMiddleware)

			r.Get("/me", s.handleMe)

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
					// Members & invitations.
					r.With(s.orgAuthz(domain.RoleMember)).Get("/members", s.handleListMembers)
					r.With(s.orgAuthz(domain.RoleAdmin)).Post("/invitations", s.handleCreateInvitation)
					r.With(s.orgAuthz(domain.RoleAdmin)).Get("/invitations", s.handleListInvitations)

					// Projects (Org → Project → App).
					r.Route("/projects", func(r chi.Router) {
						r.With(s.orgAuthz(domain.RoleMember)).Get("/", s.handleListProjects)
						r.With(s.orgAuthz(domain.RoleAdmin)).Post("/", s.handleCreateProject)
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
					})

					r.With(s.orgAuthz(domain.RoleMember)).Get("/billing", s.handleGetBilling)
					r.With(s.orgAuthz(domain.RoleAdmin)).Post("/billing/subscribe", s.handleSubscribe)
				})
			})
		})
	})

	return r
}
