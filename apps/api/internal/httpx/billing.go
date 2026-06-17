package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/billing"
)

// handlePlans returns the public plan catalog.
func (s *Server) handlePlans(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"data":     s.billing.Catalog(),
		"provider": s.billing.ProviderName(),
	})
}

// handleGetBilling returns an org's subscription + usage summary.
func (s *Server) handleGetBilling(w http.ResponseWriter, r *http.Request) {
	sum, err := s.billing.GetBilling(r.Context(), chi.URLParam(r, "orgID"))
	if err != nil {
		s.logger.Error("get billing", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to load billing")
		return
	}
	writeJSON(w, http.StatusOK, sum)
}

type subscribeRequest struct {
	PlanID string `json:"planId"`
}

// handleSubscribe subscribes the org to a plan (admin only).
func (s *Server) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req subscribeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	res, err := s.billing.Subscribe(r.Context(), chi.URLParam(r, "orgID"), req.PlanID, user.Email)
	if err != nil {
		if errors.Is(err, billing.ErrUnknownPlan) {
			writeError(w, http.StatusBadRequest, "unknown plan")
			return
		}
		s.logger.Error("subscribe", "err", err)
		writeError(w, http.StatusBadGateway, "billing provider error")
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleStripeWebhook verifies and processes Stripe webhook events.
func (s *Server) handleStripeWebhook(w http.ResponseWriter, r *http.Request) {
	secret := s.cfg.StripeWebhookSecret
	if secret == "" {
		// Webhooks are not configured (local/demo) — acknowledge without processing.
		w.WriteHeader(http.StatusOK)
		return
	}
	payload, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not read body")
		return
	}
	if err := billing.VerifyWebhookSignature(payload, r.Header.Get("Stripe-Signature"), secret, 5*time.Minute, time.Now()); err != nil {
		writeError(w, http.StatusBadRequest, "invalid signature")
		return
	}

	var evt struct {
		Type string `json:"type"`
		Data struct {
			Object struct {
				Metadata struct {
					OrgID string `json:"org_id"`
				} `json:"metadata"`
				Status string `json:"status"`
			} `json:"object"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &evt); err != nil {
		writeError(w, http.StatusBadRequest, "invalid event")
		return
	}
	if orgID := evt.Data.Object.Metadata.OrgID; orgID != "" {
		switch evt.Type {
		case "customer.subscription.updated", "customer.subscription.created", "invoice.paid":
			_ = s.billing.SetSubscriptionStatus(r.Context(), orgID, "active")
		case "customer.subscription.deleted":
			_ = s.billing.SetSubscriptionStatus(r.Context(), orgID, "canceled")
		}
	}
	w.WriteHeader(http.StatusOK)
}
