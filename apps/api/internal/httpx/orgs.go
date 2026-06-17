package httpx

import "net/http"

type createOrgRequest struct {
	Name string `json:"name"`
}

func (s *Server) handleListOrgs(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	orgs, err := s.identity.ListOrganizations(r.Context(), user.ID)
	if err != nil {
		s.logger.Error("list organizations", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to list organizations")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": orgs})
}

func (s *Server) handleCreateOrg(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req createOrgRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	org, err := s.identity.CreateOrganization(r.Context(), user.ID, req.Name)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, org)
}
