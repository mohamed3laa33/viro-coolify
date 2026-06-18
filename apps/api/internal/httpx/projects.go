package httpx

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/identity"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/platform"
)

// projectAuthz requires the caller to be able to act on {projectID} within
// {orgID} with at least the given role (org admins have full project access).
func (s *Server) projectAuthz(min domain.Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := userFromContext(r.Context())
			if !ok {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			orgID := chi.URLParam(r, "orgID")
			projectID := chi.URLParam(r, "projectID")
			if err := s.identity.AuthorizeProject(r.Context(), user.ID, orgID, projectID, min); err != nil {
				if errors.Is(err, identity.ErrForbidden) {
					writeError(w, http.StatusForbidden, "you do not have access to this project")
					return
				}
				s.logger.Error("authorize project", "err", err)
				writeError(w, http.StatusInternalServerError, "authorization error")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (s *Server) currentUser(w http.ResponseWriter, r *http.Request) (*domain.User, bool) {
	user, ok := userFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
	}
	return user, ok
}

// resourceProjectAuthz returns middleware that enforces PROJECT-membership authz
// on a resource lifecycle route. It resolves the resource's ProjectID (via
// resolveProjectID, scoped to the URL {orgID}) and then requires the caller to be
// able to act on that project with at least `min` (org admins/owners retain full
// access via AuthorizeProject's semantics; a non-member of the project is 403'd).
//
// This closes the IDOR where org-level authz let any org member read/deploy/
// delete apps/services/databases in projects they are not a member of. A resource
// not found within the org is hidden as 404 (no cross-tenant existence leak).
func (s *Server) resourceProjectAuthz(idParam string, resolve func(ctx context.Context, orgID, resourceID string) (string, bool), min domain.Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := userFromContext(r.Context())
			if !ok {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			orgID := chi.URLParam(r, "orgID")
			resourceID := chi.URLParam(r, idParam)
			projectID, found := resolve(r.Context(), orgID, resourceID)
			if !found {
				// Don't leak existence across tenants.
				writeError(w, http.StatusNotFound, "not found")
				return
			}
			if err := s.identity.AuthorizeProject(r.Context(), user.ID, orgID, projectID, min); err != nil {
				if errors.Is(err, identity.ErrForbidden) {
					writeError(w, http.StatusForbidden, "you do not have access to this project")
					return
				}
				s.logger.Error("authorize project resource", "err", err)
				writeError(w, http.StatusInternalServerError, "authorization error")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// resolveAppProject resolves an app's ProjectID, scoped to orgID. found=false
// when the app does not exist or belongs to another org.
func (s *Server) resolveAppProject(ctx context.Context, orgID, appID string) (string, bool) {
	a, err := s.store.GetApp(ctx, appID)
	if err != nil || a.OrgID != orgID {
		return "", false
	}
	return a.ProjectID, true
}

// resolveServiceProject resolves a service's ProjectID, scoped to orgID.
func (s *Server) resolveServiceProject(ctx context.Context, orgID, serviceID string) (string, bool) {
	svc, err := s.store.GetService(ctx, serviceID)
	if err != nil || svc.OrgID != orgID {
		return "", false
	}
	return svc.ProjectID, true
}

// resolveDatabaseProject resolves a database's ProjectID, scoped to orgID.
func (s *Server) resolveDatabaseProject(ctx context.Context, orgID, databaseID string) (string, bool) {
	d, err := s.store.GetDatabase(ctx, databaseID)
	if err != nil || d.OrgID != orgID {
		return "", false
	}
	return d.ProjectID, true
}

// appProjectAuthz / serviceProjectAuthz / databaseProjectAuthz are the concrete
// per-resource project-membership middlewares for the lifecycle routes.
func (s *Server) appProjectAuthz(min domain.Role) func(http.Handler) http.Handler {
	return s.resourceProjectAuthz("appID", s.resolveAppProject, min)
}

func (s *Server) serviceProjectAuthz(min domain.Role) func(http.Handler) http.Handler {
	return s.resourceProjectAuthz("serviceID", s.resolveServiceProject, min)
}

func (s *Server) databaseProjectAuthz(min domain.Role) func(http.Handler) http.Handler {
	return s.resourceProjectAuthz("databaseID", s.resolveDatabaseProject, min)
}

type createProjectRequest struct {
	Name string `json:"name"`
}

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(w, r)
	if !ok {
		return
	}
	projects, err := s.identity.ListProjects(r.Context(), user.ID, chi.URLParam(r, "orgID"))
	if err != nil {
		s.writeIdentityError(w, "list projects", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": projects})
}

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(w, r)
	if !ok {
		return
	}
	var req createProjectRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	project, err := s.identity.CreateProject(r.Context(), user.ID, chi.URLParam(r, "orgID"), req.Name)
	if err != nil {
		s.writeIdentityError(w, "create project", err)
		return
	}
	writeJSON(w, http.StatusCreated, project)
}

// handleDeleteProject deletes an empty, non-default project (org admin+). A
// project that still owns apps or services is rejected with 409.
func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(w, r)
	if !ok {
		return
	}
	if err := s.identity.DeleteProject(r.Context(), user.ID, chi.URLParam(r, "orgID"), chi.URLParam(r, "projectID")); err != nil {
		s.writeIdentityError(w, "delete project", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleCreateAppInProject creates an app inside an explicit project.
func (s *Server) handleCreateAppInProject(w http.ResponseWriter, r *http.Request) {
	var req createAppRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	app, err := s.platform.CreateApp(r.Context(), chi.URLParam(r, "orgID"), platform.CreateAppInput{
		Name:          req.Name,
		ProjectID:     chi.URLParam(r, "projectID"),
		Image:         req.Image,
		GitRepository: req.GitRepository,
		GitBranch:     req.GitBranch,
		BuildPack:     req.BuildPack,
		CPU:           req.CPU,
		MemoryMB:      req.MemoryMB,
		ProjectUUID:   req.ProjectUUID,
		ServerUUID:    req.ServerUUID,
	})
	if err != nil {
		s.writePlatformError(w, "create app in project", err)
		return
	}
	writeJSON(w, http.StatusCreated, app)
}

func (s *Server) handleListProjectApps(w http.ResponseWriter, r *http.Request) {
	apps, err := s.platform.ListAppsInProject(r.Context(), chi.URLParam(r, "orgID"), chi.URLParam(r, "projectID"))
	if err != nil {
		s.writePlatformError(w, "list project apps", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": apps})
}

func (s *Server) handleListMembers(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(w, r)
	if !ok {
		return
	}
	members, err := s.identity.ListMembers(r.Context(), user.ID, chi.URLParam(r, "orgID"))
	if err != nil {
		s.writeIdentityError(w, "list members", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": members})
}

type updateMemberRequest struct {
	Role string `json:"role"`
}

// handleUpdateMember changes a member's role within an org (org owner only).
func (s *Server) handleUpdateMember(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(w, r)
	if !ok {
		return
	}
	var req updateMemberRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	orgID, targetID := chi.URLParam(r, "orgID"), chi.URLParam(r, "userID")
	if err := s.identity.UpdateMemberRole(r.Context(), user.ID, orgID, targetID, domain.Role(req.Role)); err != nil {
		s.writeIdentityError(w, "update member", err)
		return
	}
	s.audit(r.Context(), orgID, "member.role_update", "member", targetID, "role="+req.Role)
	w.WriteHeader(http.StatusNoContent)
}

// handleRemoveMember removes a member from an org (org owner only). Removing the
// last remaining owner is rejected with 409.
func (s *Server) handleRemoveMember(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(w, r)
	if !ok {
		return
	}
	orgID, targetID := chi.URLParam(r, "orgID"), chi.URLParam(r, "userID")
	if err := s.identity.RemoveMember(r.Context(), user.ID, orgID, targetID); err != nil {
		s.writeIdentityError(w, "remove member", err)
		return
	}
	s.audit(r.Context(), orgID, "member.remove", "member", targetID, "")
	w.WriteHeader(http.StatusNoContent)
}

type inviteRequest struct {
	Email     string `json:"email"`
	Role      string `json:"role"`
	ProjectID string `json:"projectId"`
}

func (s *Server) handleCreateInvitation(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(w, r)
	if !ok {
		return
	}
	var req inviteRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	role := domain.Role(req.Role)
	if role == "" {
		role = domain.RoleMember
	}
	orgID := chi.URLParam(r, "orgID")
	inv, err := s.identity.Invite(r.Context(), user.ID, orgID, req.ProjectID, req.Email, role)
	if err != nil {
		s.writeIdentityError(w, "invite", err)
		return
	}
	// AUDIT: record the invitation id + invited email (an email is not a secret).
	s.audit(r.Context(), orgID, "invitation.create", "invitation", inv.ID, "email="+inv.Email)
	writeJSON(w, http.StatusCreated, inv)
}

func (s *Server) handleListInvitations(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(w, r)
	if !ok {
		return
	}
	invs, err := s.identity.ListInvitations(r.Context(), user.ID, chi.URLParam(r, "orgID"))
	if err != nil {
		s.writeIdentityError(w, "list invitations", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": invs})
}

// handleRevokeInvitation revokes a pending invitation (org admin+).
func (s *Server) handleRevokeInvitation(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(w, r)
	if !ok {
		return
	}
	orgID, inviteID := chi.URLParam(r, "orgID"), chi.URLParam(r, "inviteID")
	if err := s.identity.RevokeInvitation(r.Context(), user.ID, orgID, inviteID); err != nil {
		s.writeIdentityError(w, "revoke invitation", err)
		return
	}
	s.audit(r.Context(), orgID, "invitation.revoke", "invitation", inviteID, "")
	w.WriteHeader(http.StatusNoContent)
}

type acceptInvitationRequest struct {
	Token string `json:"token"`
}

func (s *Server) handleAcceptInvitation(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(w, r)
	if !ok {
		return
	}
	var req acceptInvitationRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	inv, err := s.identity.AcceptInvitation(r.Context(), user.ID, user.Email, req.Token)
	if err != nil {
		s.writeIdentityError(w, "accept invitation", err)
		return
	}
	// Audit the moment the user actually gains org membership/role (the invite
	// CREATE and REVOKE are already audited above; this closes the gap).
	s.audit(r.Context(), inv.OrgID, "member.invite_accept", "member", user.ID, "role="+string(inv.Role))
	writeJSON(w, http.StatusOK, inv)
}

// writeIdentityError maps identity errors to HTTP codes.
func (s *Server) writeIdentityError(w http.ResponseWriter, action string, err error) {
	switch {
	case errors.Is(err, identity.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, identity.ErrValidation):
		writeError(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), "identity: "))
	case errors.Is(err, identity.ErrInvitationInvalid):
		writeError(w, http.StatusBadRequest, "invitation is invalid or already used")
	case errors.Is(err, identity.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, identity.ErrConflict):
		writeError(w, http.StatusConflict, strings.TrimPrefix(err.Error(), "identity: "))
	default:
		s.logger.Error(action, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}
