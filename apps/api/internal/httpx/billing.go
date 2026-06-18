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

// handlePricing returns the public hourly price list (active components).
func (s *Server) handlePricing(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"data": s.billing.PricingComponents(r.Context()),
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

	var raw struct {
		ID   string `json:"id"`
		Type string `json:"type"`
		Data struct {
			Object struct {
				ID               string `json:"id"`
				Subscription     string `json:"subscription"`
				Customer         string `json:"customer"`
				ClientReference  string `json:"client_reference_id"`
				Status           string `json:"status"`
				CurrentPeriodEnd int64  `json:"current_period_end"`
				Metadata         struct {
					OrgID string `json:"org_id"`
				} `json:"metadata"`
				// Subscription items: on customer.subscription.* the metered item id
				// (si_) and (Stripe 2025-03+) the per-item current_period_end live here.
				Items struct {
					Data []struct {
						ID               string `json:"id"`
						CurrentPeriodEnd int64  `json:"current_period_end"`
					} `json:"data"`
				} `json:"items"`
			} `json:"object"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		writeError(w, http.StatusBadRequest, "invalid event")
		return
	}

	// First subscription item carries the metered si_ id and the per-item period end.
	var itemID string
	var itemPeriodEnd int64
	if items := raw.Data.Object.Items.Data; len(items) > 0 {
		itemID = items[0].ID
		itemPeriodEnd = items[0].CurrentPeriodEnd
	}

	evt := billing.StripeEvent{
		ID:                   raw.ID,
		Type:                 raw.Type,
		ID2:                  raw.Data.Object.ID,
		Subscription:         raw.Data.Object.Subscription,
		Customer:             raw.Data.Object.Customer,
		ClientReference:      raw.Data.Object.ClientReference,
		Status:               raw.Data.Object.Status,
		CurrentPeriodEnd:     raw.Data.Object.CurrentPeriodEnd,
		MetadataOrgID:        raw.Data.Object.Metadata.OrgID,
		SubscriptionItemID:   itemID,
		ItemCurrentPeriodEnd: itemPeriodEnd,
	}
	processed, err := s.billing.ProcessEvent(r.Context(), evt)
	if err != nil {
		// Genuine store failure: 5xx so Stripe retries (don't swallow).
		s.logger.Error("stripe webhook", "type", evt.Type, "err", err)
		writeError(w, http.StatusInternalServerError, "failed to process event")
		return
	}
	// AUDIT: record subscription status changes applied by the webhook. The actor
	// is the billing provider (no request user); platform-level (no org in the
	// webhook context). Metadata carries the mapped status — never any secret.
	if processed && isSubscriptionEvent(evt.Type) {
		s.auditAs(r.Context(), "", "stripe-webhook", "", "subscription.update", "subscription",
			evt.Subscription, "type="+evt.Type+",status="+evt.Status)
	}
	w.WriteHeader(http.StatusOK)
}

// isSubscriptionEvent reports whether a Stripe event type changes an org's
// subscription state (and so should be recorded in the audit trail).
func isSubscriptionEvent(t string) bool {
	switch t {
	case "checkout.session.completed",
		"customer.subscription.created",
		"customer.subscription.updated",
		"customer.subscription.deleted":
		return true
	default:
		return false
	}
}
