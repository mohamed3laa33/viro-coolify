package httpx

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/platform"
)

// handleServiceCatalog returns the one-click catalog. Public endpoint.
func (s *Server) handleServiceCatalog(w http.ResponseWriter, r *http.Request) {
	catalog, err := s.platform.ListCatalog(r.Context())
	if err != nil {
		s.logger.Error("list catalog", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to load catalog")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": catalog})
}

func (s *Server) handleListServices(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(w, r)
	if !ok {
		return
	}
	orgID := chi.URLParam(r, "orgID")
	svcs, err := s.platform.ListServices(r.Context(), orgID)
	if err != nil {
		s.writePlatformError(w, "list services", err)
		return
	}
	ids, isAdmin, err := s.identity.AccessibleProjectIDs(r.Context(), user.ID, orgID)
	if err != nil {
		s.logger.Error("list services: accessible projects", "err", err)
		writeError(w, http.StatusInternalServerError, "authorization error")
		return
	}
	if !isAdmin {
		filtered := svcs[:0:0]
		for _, sv := range svcs {
			if ids[sv.ProjectID] {
				filtered = append(filtered, sv)
			}
		}
		svcs = filtered
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": svcs})
}

type createServiceRequest struct {
	TemplateKey string  `json:"templateKey"`
	Name        string  `json:"name"`
	Image       string  `json:"image"`
	CPU         float64 `json:"cpu"`
	MemoryMB    int     `json:"memoryMb"`
	Region      string  `json:"region"`
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
		Image:       req.Image,
		CPU:         req.CPU,
		MemoryMB:    req.MemoryMB,
		Region:      req.Region,
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
