package httpx

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (s *Server) handleListAppEnv(w http.ResponseWriter, r *http.Request) {
	env, err := s.platform.ListEnv(r.Context(), chi.URLParam(r, "orgID"), chi.URLParam(r, "appID"))
	if err != nil {
		s.writePlatformError(w, "list app env", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": env})
}

type setEnvRequest struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func (s *Server) handleSetAppEnv(w http.ResponseWriter, r *http.Request) {
	var req setEnvRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Key == "" {
		writeError(w, http.StatusBadRequest, "key is required")
		return
	}
	env, err := s.platform.SetEnv(r.Context(), chi.URLParam(r, "orgID"), chi.URLParam(r, "appID"), req.Key, req.Value)
	if err != nil {
		s.writePlatformError(w, "set app env", err)
		return
	}
	writeJSON(w, http.StatusOK, env)
}

func (s *Server) handleDeleteAppEnv(w http.ResponseWriter, r *http.Request) {
	if err := s.platform.DeleteEnv(r.Context(), chi.URLParam(r, "orgID"), chi.URLParam(r, "appID"), chi.URLParam(r, "key")); err != nil {
		s.writePlatformError(w, "delete app env", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListAppDomains(w http.ResponseWriter, r *http.Request) {
	domains, err := s.platform.ListDomains(r.Context(), chi.URLParam(r, "orgID"), chi.URLParam(r, "appID"))
	if err != nil {
		s.writePlatformError(w, "list app domains", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": domains})
}

type addDomainRequest struct {
	Domain string `json:"domain"`
}

func (s *Server) handleAddAppDomain(w http.ResponseWriter, r *http.Request) {
	var req addDomainRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Domain == "" {
		writeError(w, http.StatusBadRequest, "domain is required")
		return
	}
	d, err := s.platform.AddDomain(r.Context(), chi.URLParam(r, "orgID"), chi.URLParam(r, "appID"), req.Domain)
	if err != nil {
		s.writePlatformError(w, "add app domain", err)
		return
	}
	writeJSON(w, http.StatusCreated, d)
}

func (s *Server) handleDeleteAppDomain(w http.ResponseWriter, r *http.Request) {
	if err := s.platform.DeleteDomain(r.Context(), chi.URLParam(r, "orgID"), chi.URLParam(r, "appID"), chi.URLParam(r, "domainID")); err != nil {
		s.writePlatformError(w, "delete app domain", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAppMetrics(w http.ResponseWriter, r *http.Request) {
	m, err := s.platform.AppMetrics(r.Context(), chi.URLParam(r, "orgID"), chi.URLParam(r, "appID"))
	if err != nil {
		s.writePlatformError(w, "app metrics", err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}
