package httpx

import (
	"context"
	"net/http"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/version"
)

// Pinger is optionally implemented by a store to report dependency health for
// readiness probes. The in-memory store does not implement it (always ready).
type Pinger interface {
	Ping(ctx context.Context) error
}

// handleHealth is the liveness probe: a static ok (the process is up).
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleReady is the readiness probe: it verifies dependency health (the store)
// when the store implements Pinger, returning 503 when a dependency is down.
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.store.(Pinger); ok {
		if err := p.Ping(r.Context()); err != nil {
			s.logger.Warn("readiness check failed", "err", err)
			writeError(w, http.StatusServiceUnavailable, "not ready")
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"service": "vortex-api",
		"version": version.Version,
		"commit":  version.Commit,
		"env":     s.cfg.Env,
	})
}
