// Package httpx wires the Viro control-plane HTTP API: router, middleware and handlers.
package httpx

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/auth"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/config"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/coolify"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/identity"
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
	s := &Server{
		cfg:      cfg,
		logger:   logger,
		coolify:  coolify.NewClient(cfg.CoolifyBaseURL, cfg.CoolifyToken),
		store:    st,
		tokens:   tokens,
		identity: identity.NewService(st, tokens),
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

		// Authenticated endpoints.
		r.Group(func(r chi.Router) {
			r.Use(s.authMiddleware)

			r.Get("/me", s.handleMe)

			r.Route("/orgs", func(r chi.Router) {
				r.Get("/", s.handleListOrgs)
				r.Post("/", s.handleCreateOrg)
			})

			r.Route("/apps", func(r chi.Router) {
				r.Get("/", s.handleListApps)
				r.Get("/{uuid}", s.handleGetApp)
				r.Post("/{uuid}/deploy", s.handleDeployApp)
				r.Post("/{uuid}/stop", s.handleStopApp)
				r.Post("/{uuid}/restart", s.handleRestartApp)
			})

			r.Get("/databases", s.handleListDatabases)
		})
	})

	return r
}
