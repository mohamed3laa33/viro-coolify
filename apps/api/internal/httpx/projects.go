package httpx

import (
	"errors"
	"net/http"

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
		GitRepository: req.GitRepository,
		GitBranch:     req.GitBranch,
		BuildPack:     req.BuildPack,
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
	inv, err := s.identity.Invite(r.Context(), user.ID, chi.URLParam(r, "orgID"), req.ProjectID, req.Email, role)
	if err != nil {
		s.writeIdentityError(w, "invite", err)
		return
	}
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
	writeJSON(w, http.StatusOK, inv)
}

// writeIdentityError maps identity errors to HTTP codes.
func (s *Server) writeIdentityError(w http.ResponseWriter, action string, err error) {
	switch {
	case errors.Is(err, identity.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, identity.ErrValidation):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, identity.ErrInvitationInvalid):
		writeError(w, http.StatusBadRequest, "invitation is invalid or already used")
	default:
		s.logger.Error(action, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}
