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
	Key    string `json:"key"`
	Value  string `json:"value"`
	Secret bool   `json:"secret"`
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
	orgID, appID := chi.URLParam(r, "orgID"), chi.URLParam(r, "appID")
	env, err := s.platform.SetEnv(r.Context(), orgID, appID, req.Key, req.Value, req.Secret)
	if err != nil {
		s.writePlatformError(w, "set app env", err)
		return
	}
	// AUDIT: record the KEY only — never the value (secret or plain).
	action := "env.set"
	if req.Secret {
		action = "secret.set"
	}
	s.audit(r.Context(), orgID, action, "app_env", appID+"/"+req.Key, "")
	writeJSON(w, http.StatusOK, env)
}

func (s *Server) handleDeleteAppEnv(w http.ResponseWriter, r *http.Request) {
	orgID, appID, key := chi.URLParam(r, "orgID"), chi.URLParam(r, "appID"), chi.URLParam(r, "key")
	if err := s.platform.DeleteEnv(r.Context(), orgID, appID, key); err != nil {
		s.writePlatformError(w, "delete app env", err)
		return
	}
	s.audit(r.Context(), orgID, "secret.delete", "app_env", appID+"/"+key, "")
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
	orgID, appID := chi.URLParam(r, "orgID"), chi.URLParam(r, "appID")
	d, err := s.platform.AddDomain(r.Context(), orgID, appID, req.Domain)
	if err != nil {
		s.writePlatformError(w, "add app domain", err)
		return
	}
	s.audit(r.Context(), orgID, "domain.add", "domain", appID+"/"+d.Domain.Domain, "")
	writeJSON(w, http.StatusCreated, d)
}

func (s *Server) handleVerifyAppDomain(w http.ResponseWriter, r *http.Request) {
	orgID, appID, domainID := chi.URLParam(r, "orgID"), chi.URLParam(r, "appID"), chi.URLParam(r, "domainID")
	d, err := s.platform.VerifyDomain(r.Context(), orgID, appID, domainID)
	if err != nil {
		s.writePlatformError(w, "verify app domain", err)
		return
	}
	s.audit(r.Context(), orgID, "domain.verify", "domain", appID+"/"+d.Domain.Domain, string(d.Status))
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) handleDeleteAppDomain(w http.ResponseWriter, r *http.Request) {
	orgID, appID, domainID := chi.URLParam(r, "orgID"), chi.URLParam(r, "appID"), chi.URLParam(r, "domainID")
	if err := s.platform.DeleteDomain(r.Context(), orgID, appID, domainID); err != nil {
		s.writePlatformError(w, "delete app domain", err)
		return
	}
	s.audit(r.Context(), orgID, "domain.delete", "domain", appID+"/"+domainID, "")
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
