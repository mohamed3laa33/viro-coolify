package httpx

import (
	"context"
	"crypto/subtle"
	"io"
	"net/http"
	"strings"

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

// handleMetrics serves the control-plane Prometheus metrics in the 0.0.4 text
// exposition format. EXPOSURE: it is internal-only and gated. When
// VORTEX_METRICS_TOKEN is configured, a request must present a matching
// `Authorization: Bearer <token>` (constant-time compared); without the token set,
// the endpoint serves unauthenticated so it can be bound to a private listen
// addr / scraped over the cluster network only. It never returns tenant data.
// handleMetrics serves the PUBLIC /metrics route. It FAILS CLOSED: with no
// VORTEX_METRICS_TOKEN configured it returns 404 (metrics are never exposed
// unauthenticated on the public API surface); with a token it requires a matching
// Bearer. The private listener (MetricsHandler) serves without this gate.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	tok := s.cfg.MetricsToken
	if tok == "" {
		http.NotFound(w, r)
		return
	}
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if subtle.ConstantTimeCompare([]byte(got), []byte(tok)) != 1 {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	s.writeMetrics(w)
}

// writeMetrics renders the Prometheus text exposition format.
func (s *Server) writeMetrics(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, s.metrics.render())
}

func (s *Server) handleVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"service": "vortex-api",
		"version": version.Version,
		"commit":  version.Commit,
		"env":     s.cfg.Env,
	})
}
