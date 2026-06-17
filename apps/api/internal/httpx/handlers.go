package httpx

import (
	"net/http"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/version"
)

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
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
