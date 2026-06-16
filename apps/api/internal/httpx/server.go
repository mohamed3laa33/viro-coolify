// Package httpx wires the Viro control-plane HTTP API: router, middleware and handlers.
package httpx

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/config"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/coolify"
)

// Server holds the API dependencies and the composed router.
type Server struct {
	cfg     *config.Config
	logger  *slog.Logger
	coolify *coolify.Client
	router  chi.Router
}

// NewServer constructs a Server with its dependencies and routes wired up.
func NewServer(cfg *config.Config, logger *slog.Logger) *Server {
	s := &Server{
		cfg:     cfg,
		logger:  logger,
		coolify: coolify.NewClient(cfg.CoolifyBaseURL, cfg.CoolifyToken),
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
	r.Use(middleware.Recoverer)
	r.Use(requestLogger(s.logger))
	r.Use(corsMiddleware(s.cfg.CORSAllowedOrigins))

	r.Get("/healthz", s.handleHealth)
	r.Get("/readyz", s.handleHealth)

	r.Route("/v1", func(r chi.Router) {
		r.Get("/version", s.handleVersion)

		r.Route("/apps", func(r chi.Router) {
			r.Get("/", s.handleListApps)
			r.Get("/{uuid}", s.handleGetApp)
			r.Post("/{uuid}/deploy", s.handleDeployApp)
			r.Post("/{uuid}/stop", s.handleStopApp)
			r.Post("/{uuid}/restart", s.handleRestartApp)
		})

		r.Get("/databases", s.handleListDatabases)
	})

	return r
}
