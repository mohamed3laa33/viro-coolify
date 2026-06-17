package httpx

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/platform"
)

// handleServiceCatalog returns the one-click catalog. Public endpoint.
func (s *Server) handleServiceCatalog(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"data": s.platform.ListCatalog(r.Context())})
}

func (s *Server) handleListServices(w http.ResponseWriter, r *http.Request) {
	svcs, err := s.platform.ListServices(r.Context(), chi.URLParam(r, "orgID"))
	if err != nil {
		s.writePlatformError(w, "list services", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": svcs})
}

type createServiceRequest struct {
	TemplateKey string  `json:"templateKey"`
	Name        string  `json:"name"`
	CPU         float64 `json:"cpu"`
	MemoryMB    int     `json:"memoryMb"`
	ProjectUUID string  `json:"projectUuid"`
	ServerUUID  string  `json:"serverUuid"`
}

func (s *Server) handleCreateService(w http.ResponseWriter, r *http.Request) {
	var req createServiceRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.TemplateKey == "" {
		writeError(w, http.StatusBadRequest, "templateKey is required")
		return
	}
	svc, err := s.platform.CreateService(r.Context(), chi.URLParam(r, "orgID"), chi.URLParam(r, "projectID"), platform.CreateServiceInput{
		TemplateKey: req.TemplateKey,
		Name:        req.Name,
		CPU:         req.CPU,
		MemoryMB:    req.MemoryMB,
		ProjectUUID: req.ProjectUUID,
		ServerUUID:  req.ServerUUID,
	})
	if err != nil {
		s.writePlatformError(w, "create service", err)
		return
	}
	writeJSON(w, http.StatusCreated, svc)
}

func (s *Server) handleDeployService(w http.ResponseWriter, r *http.Request) {
	svc, err := s.platform.DeployService(r.Context(), chi.URLParam(r, "orgID"), chi.URLParam(r, "serviceID"))
	if err != nil {
		s.writePlatformError(w, "deploy service", err)
		return
	}
	writeJSON(w, http.StatusAccepted, svc)
}

func (s *Server) handleStopService(w http.ResponseWriter, r *http.Request) {
	svc, err := s.platform.StopService(r.Context(), chi.URLParam(r, "orgID"), chi.URLParam(r, "serviceID"))
	if err != nil {
		s.writePlatformError(w, "stop service", err)
		return
	}
	writeJSON(w, http.StatusAccepted, svc)
}

func (s *Server) handleRestartService(w http.ResponseWriter, r *http.Request) {
	svc, err := s.platform.RestartService(r.Context(), chi.URLParam(r, "orgID"), chi.URLParam(r, "serviceID"))
	if err != nil {
		s.writePlatformError(w, "restart service", err)
		return
	}
	writeJSON(w, http.StatusAccepted, svc)
}

func (s *Server) handleDeleteService(w http.ResponseWriter, r *http.Request) {
	if err := s.platform.DeleteService(r.Context(), chi.URLParam(r, "orgID"), chi.URLParam(r, "serviceID")); err != nil {
		s.writePlatformError(w, "delete service", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
