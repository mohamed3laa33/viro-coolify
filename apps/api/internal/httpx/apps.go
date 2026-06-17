package httpx

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/identity"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/platform"
)

// orgAuthz returns middleware that requires the caller to be a member of the
// {orgID} organization with at least the given role.
func (s *Server) orgAuthz(min domain.Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := userFromContext(r.Context())
			if !ok {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			orgID := chi.URLParam(r, "orgID")
			if _, err := s.identity.Authorize(r.Context(), user.ID, orgID, min); err != nil {
				if errors.Is(err, identity.ErrForbidden) {
					writeError(w, http.StatusForbidden, "you do not have access to this organization")
					return
				}
				s.logger.Error("authorize", "err", err)
				writeError(w, http.StatusInternalServerError, "authorization error")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// writePlatformError maps platform/coolify errors to HTTP codes.
func (s *Server) writePlatformError(w http.ResponseWriter, action string, err error) {
	switch {
	case errors.Is(err, platform.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, platform.ErrQuotaExceeded):
		writeError(w, http.StatusPaymentRequired, err.Error())
	case errors.Is(err, platform.ErrInvalidTemplate):
		writeError(w, http.StatusBadRequest, "unknown catalog template")
	default:
		s.logger.Error(action, "err", err)
		writeError(w, http.StatusBadGateway, "upstream error from deploy backend")
	}
}

type createAppRequest struct {
	Name          string  `json:"name"`
	ProjectID     string  `json:"projectId"`
	GitRepository string  `json:"gitRepository"`
	GitBranch     string  `json:"gitBranch"`
	BuildPack     string  `json:"buildPack"`
	CPU           float64 `json:"cpu"`
	MemoryMB      int     `json:"memoryMb"`
	ProjectUUID   string  `json:"projectUuid"`
	ServerUUID    string  `json:"serverUuid"`
}

func (s *Server) handleListApps(w http.ResponseWriter, r *http.Request) {
	apps, err := s.platform.ListApps(r.Context(), chi.URLParam(r, "orgID"))
	if err != nil {
		s.writePlatformError(w, "list apps", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": apps})
}

func (s *Server) handleCreateApp(w http.ResponseWriter, r *http.Request) {
	var req createAppRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	orgID := chi.URLParam(r, "orgID")
	// Apps belong to a project; default to the org's default project when unspecified.
	projectID := req.ProjectID
	if projectID == "" {
		if p, err := s.identity.DefaultProject(r.Context(), orgID); err == nil {
			projectID = p.ID
		}
	}
	app, err := s.platform.CreateApp(r.Context(), orgID, platform.CreateAppInput{
		Name:          req.Name,
		ProjectID:     projectID,
		GitRepository: req.GitRepository,
		GitBranch:     req.GitBranch,
		BuildPack:     req.BuildPack,
		CPU:           req.CPU,
		MemoryMB:      req.MemoryMB,
		ProjectUUID:   req.ProjectUUID,
		ServerUUID:    req.ServerUUID,
	})
	if err != nil {
		s.writePlatformError(w, "create app", err)
		return
	}
	writeJSON(w, http.StatusCreated, app)
}

func (s *Server) handleGetApp(w http.ResponseWriter, r *http.Request) {
	app, err := s.platform.GetApp(r.Context(), chi.URLParam(r, "orgID"), chi.URLParam(r, "appID"))
	if err != nil {
		s.writePlatformError(w, "get app", err)
		return
	}
	writeJSON(w, http.StatusOK, app)
}

func (s *Server) handleDeleteApp(w http.ResponseWriter, r *http.Request) {
	if err := s.platform.Delete(r.Context(), chi.URLParam(r, "orgID"), chi.URLParam(r, "appID")); err != nil {
		s.writePlatformError(w, "delete app", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeployApp(w http.ResponseWriter, r *http.Request) {
	app, err := s.platform.Deploy(r.Context(), chi.URLParam(r, "orgID"), chi.URLParam(r, "appID"))
	if err != nil {
		s.writePlatformError(w, "deploy app", err)
		return
	}
	writeJSON(w, http.StatusAccepted, app)
}

func (s *Server) handleStopApp(w http.ResponseWriter, r *http.Request) {
	app, err := s.platform.Stop(r.Context(), chi.URLParam(r, "orgID"), chi.URLParam(r, "appID"))
	if err != nil {
		s.writePlatformError(w, "stop app", err)
		return
	}
	writeJSON(w, http.StatusAccepted, app)
}

func (s *Server) handleRestartApp(w http.ResponseWriter, r *http.Request) {
	app, err := s.platform.Restart(r.Context(), chi.URLParam(r, "orgID"), chi.URLParam(r, "appID"))
	if err != nil {
		s.writePlatformError(w, "restart app", err)
		return
	}
	writeJSON(w, http.StatusAccepted, app)
}

func (s *Server) handleAppLogs(w http.ResponseWriter, r *http.Request) {
	logs, err := s.platform.AppLogs(r.Context(), chi.URLParam(r, "orgID"), chi.URLParam(r, "appID"))
	if err != nil {
		s.writePlatformError(w, "app logs", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"logs": logs})
}

type createDatabaseRequest struct {
	Name   string `json:"name"`
	Engine string `json:"engine"`
}

func (s *Server) handleListDatabases(w http.ResponseWriter, r *http.Request) {
	dbs, err := s.platform.ListDatabases(r.Context(), chi.URLParam(r, "orgID"))
	if err != nil {
		s.writePlatformError(w, "list databases", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": dbs})
}

func (s *Server) handleCreateDatabase(w http.ResponseWriter, r *http.Request) {
	var req createDatabaseRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	db, err := s.platform.CreateDatabase(r.Context(), chi.URLParam(r, "orgID"), platform.CreateDatabaseInput{
		Name:   req.Name,
		Engine: req.Engine,
	})
	if err != nil {
		s.writePlatformError(w, "create database", err)
		return
	}
	writeJSON(w, http.StatusCreated, db)
}
