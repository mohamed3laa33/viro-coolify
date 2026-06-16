package httpx

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/version"
)

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"service": "viro-api",
		"version": version.Version,
		"commit":  version.Commit,
		"env":     s.cfg.Env,
	})
}

func (s *Server) handleListApps(w http.ResponseWriter, r *http.Request) {
	apps, err := s.coolify.ListApplications(r.Context())
	if err != nil {
		s.logger.Error("coolify list applications", "err", err)
		writeError(w, http.StatusBadGateway, "failed to list applications")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": apps})
}

func (s *Server) handleGetApp(w http.ResponseWriter, r *http.Request) {
	app, err := s.coolify.GetApplication(r.Context(), chi.URLParam(r, "uuid"))
	if err != nil {
		s.logger.Error("coolify get application", "err", err)
		writeError(w, http.StatusBadGateway, "failed to get application")
		return
	}
	writeJSON(w, http.StatusOK, app)
}

func (s *Server) handleDeployApp(w http.ResponseWriter, r *http.Request) {
	if err := s.coolify.StartApplication(r.Context(), chi.URLParam(r, "uuid")); err != nil {
		s.logger.Error("coolify deploy application", "err", err)
		writeError(w, http.StatusBadGateway, "failed to deploy application")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "deploying"})
}

func (s *Server) handleStopApp(w http.ResponseWriter, r *http.Request) {
	if err := s.coolify.StopApplication(r.Context(), chi.URLParam(r, "uuid")); err != nil {
		s.logger.Error("coolify stop application", "err", err)
		writeError(w, http.StatusBadGateway, "failed to stop application")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "stopping"})
}

func (s *Server) handleRestartApp(w http.ResponseWriter, r *http.Request) {
	if err := s.coolify.RestartApplication(r.Context(), chi.URLParam(r, "uuid")); err != nil {
		s.logger.Error("coolify restart application", "err", err)
		writeError(w, http.StatusBadGateway, "failed to restart application")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "restarting"})
}

func (s *Server) handleListDatabases(w http.ResponseWriter, r *http.Request) {
	dbs, err := s.coolify.ListDatabases(r.Context())
	if err != nil {
		s.logger.Error("coolify list databases", "err", err)
		writeError(w, http.StatusBadGateway, "failed to list databases")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": dbs})
}
