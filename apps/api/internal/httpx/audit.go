package httpx

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
)

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

// handleAdminAudit returns a bounded page of platform-level (super-admin) audit
// events, newest first. Secret values are never recorded, so the listing is safe
// to return.
func (s *Server) handleAdminAudit(w http.ResponseWriter, r *http.Request) {
	s.listAudit(w, r, "")
}

// handleOrgAudit returns a bounded page of an org's audit events (org admin+).
func (s *Server) handleOrgAudit(w http.ResponseWriter, r *http.Request) {
	s.listAudit(w, r, chi.URLParam(r, "orgID"))
}

// listAudit serves a paginated audit listing scoped to orgID ("" => platform).
func (s *Server) listAudit(w http.ResponseWriter, r *http.Request, orgID string) {
	page := parsePage(r)
	filter := domain.AuditFilter{OrgID: orgID, Limit: page.Limit, Offset: page.Offset}
	events, err := s.store.ListAuditEvents(r.Context(), filter)
	if err != nil {
		s.logger.Error("list audit", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to list audit events")
		return
	}
	total, err := s.store.CountAuditEvents(r.Context(), filter)
	if err != nil {
		s.logger.Error("count audit", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to list audit events")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": events,
		"page": pageMeta(page, len(events), total),
	})
}
