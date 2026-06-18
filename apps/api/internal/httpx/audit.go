package httpx

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
)

// auditDefaultLimit / auditMaxLimit bound the audit listing page size.
const (
	auditDefaultLimit = 100
	auditMaxLimit     = 500
)

// auditPageSize reads the ?limit query param, clamped to [1, auditMaxLimit] with
// a default of auditDefaultLimit.
func auditPageSize(r *http.Request) int {
	n, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || n <= 0 {
		return auditDefaultLimit
	}
	if n > auditMaxLimit {
		return auditMaxLimit
	}
	return n
}

// audit appends one audit event, resolving the actor from the request context.
// It is best-effort: an audit write failure is LOGGED but never fails the
// originating request (the privileged mutation already succeeded). Callers must
// pass only NON-secret identifiers in targetID/metadata — never a secret value.
//
// orgID scopes the event to an organization; pass "" for platform-level
// (super-admin / auth) events. For auth events where there is no authenticated
// context user yet (login/login_failed), use auditActor to supply the actor.
func (s *Server) audit(ctx context.Context, orgID, action, targetType, targetID, metadata string) {
	actorID, actorEmail := "", ""
	if u, ok := userFromContext(ctx); ok && u != nil {
		actorID, actorEmail = u.ID, u.Email
	}
	s.auditAs(ctx, actorID, actorEmail, orgID, action, targetType, targetID, metadata)
}

// auditAs appends one audit event with an explicit actor (used for auth events,
// where the actor is resolved from the credentials rather than the request
// context). Best-effort like audit.
func (s *Server) auditAs(ctx context.Context, actorID, actorEmail, orgID, action, targetType, targetID, metadata string) {
	e := &domain.AuditEvent{
		ID:          uuid.NewString(),
		OrgID:       orgID,
		ActorUserID: actorID,
		ActorEmail:  actorEmail,
		Action:      action,
		TargetType:  targetType,
		TargetID:    targetID,
		Metadata:    metadata,
		At:          time.Now(),
	}
	if err := s.store.CreateAuditEvent(ctx, e); err != nil {
		s.logger.Error("audit: record event", "action", action, "err", err)
	}
}

// handleAdminAudit returns the most-recent platform-level (super-admin) audit
// events. Secret values are never recorded, so the listing is safe to return.
func (s *Server) handleAdminAudit(w http.ResponseWriter, r *http.Request) {
	events, err := s.store.ListAuditEvents(r.Context(), domain.AuditFilter{OrgID: "", Limit: auditPageSize(r)})
	if err != nil {
		s.logger.Error("admin audit", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to list audit events")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": events})
}

// handleOrgAudit returns the most-recent audit events for an org (org admin+).
func (s *Server) handleOrgAudit(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "orgID")
	events, err := s.store.ListAuditEvents(r.Context(), domain.AuditFilter{OrgID: orgID, Limit: auditPageSize(r)})
	if err != nil {
		s.logger.Error("org audit", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to list audit events")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": events})
}
