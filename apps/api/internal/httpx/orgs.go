package httpx

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/identity"
)

type createOrgRequest struct {
	Name string `json:"name"`
}

// updateOrgRequest carries the editable org fields. Pointers distinguish an
// omitted field (leave unchanged) from one explicitly set (including to empty).
type updateOrgRequest struct {
	Name         *string `json:"name"`
	BillingEmail *string `json:"billingEmail"`
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

// handleUpdateOrg edits an org's name and/or billing email (org admin+).
func (s *Server) handleUpdateOrg(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(w, r)
	if !ok {
		return
	}
	var req updateOrgRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == nil && req.BillingEmail == nil {
		writeError(w, http.StatusBadRequest, "no fields to update")
		return
	}
	org, err := s.identity.UpdateOrganization(r.Context(), user.ID, chi.URLParam(r, "orgID"), identity.UpdateOrgInput{
		Name:         req.Name,
		BillingEmail: req.BillingEmail,
	})
	if err != nil {
		s.writeIdentityError(w, "update organization", err)
		return
	}
	writeJSON(w, http.StatusOK, org)
}
