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
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/coolify"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/identity"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/platform"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/store"
)

// Server holds the API dependencies and the composed router.
type Server struct {
	cfg      *config.Config
	logger   *slog.Logger
	coolify  *coolify.Client
	store    store.Store
	tokens   *auth.TokenManager
	identity *identity.Service
	platform *platform.Service
	billing  *billing.Service
	router   chi.Router
}

// NewServer constructs a Server with its dependencies and routes wired up.
//
// The control-plane store defaults to an in-memory implementation (great for
// local development and tests); a Postgres store satisfies the same interface
// and is swapped in by configuration.
func NewServer(cfg *config.Config, logger *slog.Logger) *Server {
	st := store.NewMemoryStore()
	tokens := auth.NewTokenManager(
		cfg.JWTSecret,
		time.Duration(cfg.JWTAccessTTL)*time.Minute,
		time.Duration(cfg.JWTRefreshTTL)*time.Hour,
	)
	cool := coolify.NewClient(cfg.CoolifyBaseURL, cfg.CoolifyToken)

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

	s := &Server{
		cfg:      cfg,
		logger:   logger,
		coolify:  cool,
		store:    st,
		tokens:   tokens,
		identity: identity.NewService(st, tokens),
		platform: platform.NewService(st, cool),
		billing:  billing.NewService(st, provider),
	}
	s.router = s.routes()
	return s
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

		// Authenticated endpoints.
		r.Group(func(r chi.Router) {
			r.Use(s.authMiddleware)

			r.Get("/me", s.handleMe)

			// Accept an invitation (to an org or a project) as the current user.
			r.Post("/invitations/accept", s.handleAcceptInvitation)

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
