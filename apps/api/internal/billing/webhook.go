package billing

import (
	"context"
	"errors"
	"time"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/store"
)

// StripeEvent is the minimal, decoded view of a Stripe webhook event the service
// needs to drive the subscription state machine. The HTTP layer verifies the
// signature, decodes the JSON into this shape, and hands it to ProcessEvent.
type StripeEvent struct {
	ID   string
	Type string
	// Object fields (data.object). Different event types populate different ones:
	//   * checkout.session.completed: Subscription (sub_ id), Customer, ClientRef.
	//   * customer.subscription.*: ID (sub_ id), Customer, Status, CurrentPeriodEnd,
	//     Metadata.OrgID, SubscriptionItemID, ItemCurrentPeriodEnd.
	ID2              string // data.object.id (subscription id for subscription events)
	Subscription     string // data.object.subscription (set on checkout.session.completed)
	Customer         string
	ClientReference  string // data.object.client_reference_id (org id on checkout session)
	Status           string // Stripe subscription status (active/past_due/…)
	CurrentPeriodEnd int64  // data.object.current_period_end (unix s); 0 when absent
	MetadataOrgID    string // data.object.metadata.org_id

	// SubscriptionItemID is the metered subscription-ITEM id (si_…) taken from the
	// subscription's items.data[0].id on customer.subscription.* events. Stripe's
	// per-item usage_records endpoint requires the si_ id (NOT the sub_ id), so this
	// is what ReportUsage must hand to the provider.
	SubscriptionItemID string
	// ItemCurrentPeriodEnd is items.data[0].current_period_end (unix s). Stripe
	// (2025-03+) moved the period boundary under each item; it is the fallback when
	// the top-level data.object.current_period_end is absent (e.g. a Checkout
	// Session has no current_period_end at all).
	ItemCurrentPeriodEnd int64
}

// periodEnd returns the effective current_period_end unix seconds: the top-level
// data.object.current_period_end when present, else the per-item fallback
// (items.data[].current_period_end, Stripe 2025-03+). 0 when neither is set.
func (e StripeEvent) periodEnd() int64 {
	if e.CurrentPeriodEnd > 0 {
		return e.CurrentPeriodEnd
	}
	return e.ItemCurrentPeriodEnd
}

// ProcessEvent applies one Stripe webhook event to the org's subscription,
// idempotently. It returns (processed=false) without error when the event id has
// already been handled (a Stripe redelivery), so the HTTP layer can 200 it.
//
// IDEMPOTENCY ORDER (critical): the event is APPLIED FIRST and the event id is
// MarkEventProcessed only AFTER a successful apply. If apply fails, the id is NOT
// marked, the error is returned (-> 5xx), and Stripe's retry re-applies it. Marking
// before applying would let a transient apply failure permanently drop the event:
// the retry would see "already processed" and skip it.
//
// Applying is idempotent on its own — each handler does an upsert of the org's
// subscription keyed by org id, so a redelivery that slips through (apply succeeded
// but MarkEventProcessed failed) re-applies the SAME terminal state harmlessly. The
// dedupe check is a fast path that avoids re-doing work on the common redelivery.
//
// An event that cannot be mapped to an org is treated as a successful no-op
// (processed=true) — there is nothing to retry — and IS marked processed.
func (s *Service) ProcessEvent(ctx context.Context, evt StripeEvent) (processed bool, err error) {
	// Fast-path dedupe: if we've already recorded this id, skip re-applying. This is
	// only a peek; the authoritative mark happens AFTER a successful apply below, so
	// a crash between apply and mark just causes a safe idempotent re-apply.
	if evt.ID != "" {
		if seen, err := s.store.EventProcessed(ctx, evt.ID); err != nil {
			return false, err // genuine store failure -> 5xx, Stripe retries
		} else if seen {
			return false, nil // already handled -> idempotent no-op
		}
	}

	switch evt.Type {
	case "checkout.session.completed":
		if err := s.applyCheckoutCompleted(ctx, evt); err != nil {
			return false, err // do NOT mark processed; let Stripe retry
		}
	case "customer.subscription.created",
		"customer.subscription.updated",
		"customer.subscription.deleted":
		if err := s.applySubscriptionEvent(ctx, evt); err != nil {
			return false, err // do NOT mark processed; let Stripe retry
		}
	default:
		// Unhandled event type: acknowledged, nothing to do (still mark it).
	}

	// Apply succeeded (or there was nothing to apply): now record the id so a future
	// redelivery is deduped. A failure HERE is surfaced so Stripe retries; the retry
	// re-applies the same terminal state idempotently, then marks it.
	if evt.ID != "" {
		if _, err := s.store.MarkEventProcessed(ctx, evt.ID); err != nil {
			return false, err
		}
	}
	return true, nil
}

// applyCheckoutCompleted captures the real subscription id from a completed
// checkout session and activates the org's subscription. The org is resolved via
// client_reference_id (preferred) or the stored customer id.
//
// A checkout.session.completed event's data.object is a Checkout SESSION, whose
// `status` ("complete") is a SESSION status — NOT a subscription status. Mapping it
// through SubscriptionStatusFromStripe would fall to SubIncomplete and leave a paid
// customer stuck incomplete. So a completed checkout sets SubActive directly; the
// real subscription status (and current_period_end / the si_ item id) arrives with
// the customer.subscription.* events. We therefore do NOT persist a period end here
// (the Subscribe placeholder stands until a real subscription event lands).
func (s *Service) applyCheckoutCompleted(ctx context.Context, evt StripeEvent) error {
	sub, err := s.resolveOrg(ctx, evt.ClientReference, evt.MetadataOrgID, evt.Customer, "")
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil // cannot map -> no-op (don't make Stripe retry forever)
		}
		return err
	}
	if evt.Subscription != "" {
		sub.StripeSubscriptionID = evt.Subscription
	}
	if evt.Customer != "" {
		sub.StripeCustomerID = evt.Customer
	}
	// A completed checkout means the subscription is active unless a later
	// customer.subscription.* event says otherwise. Never map the SESSION status.
	sub.Status = domain.SubActive
	return s.store.UpsertSubscription(ctx, sub)
}

// applySubscriptionEvent maps a customer.subscription.* event's REAL status onto
// the org's subscription (never forced to active). deleted events map to canceled.
// This is the authoritative source for CurrentPeriodEnd and the metered
// subscription-item id (si_): both are read from the subscription object (the item
// id and, on Stripe 2025-03+, the per-item current_period_end).
func (s *Service) applySubscriptionEvent(ctx context.Context, evt StripeEvent) error {
	subID := evt.ID2
	sub, err := s.resolveOrg(ctx, evt.MetadataOrgID, "", evt.Customer, subID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return err
	}
	status := domain.SubscriptionStatusFromStripe(evt.Status)
	if evt.Type == "customer.subscription.deleted" {
		status = domain.SubCanceled
	}
	sub.Status = status
	if subID != "" {
		sub.StripeSubscriptionID = subID
	}
	if evt.Customer != "" {
		sub.StripeCustomerID = evt.Customer
	}
	// Persist the metered subscription-ITEM id (si_) so ReportUsage hits the per-item
	// usage endpoint (which 404s on a sub_ id).
	if evt.SubscriptionItemID != "" {
		sub.StripeSubscriptionItemID = evt.SubscriptionItemID
	}
	// current_period_end: prefer the top-level field, fall back to the per-item one
	// (Stripe 2025-03+ moved it under items.data[]).
	if pe := evt.periodEnd(); pe > 0 {
		sub.CurrentPeriodEnd = time.Unix(pe, 0).UTC()
	}
	return s.store.UpsertSubscription(ctx, sub)
}

// resolveOrg finds the org's subscription by trying, in order: explicit org ids
// (metadata / client_reference_id), the Stripe subscription id, then the customer
// id. Returns store.ErrNotFound when none resolve.
func (s *Service) resolveOrg(ctx context.Context, orgID1, orgID2, customerID, subID string) (*domain.Subscription, error) {
	for _, id := range []string{orgID1, orgID2} {
		if id == "" {
			continue
		}
		if sub, err := s.store.GetSubscription(ctx, id); err == nil {
			return sub, nil
		}
	}
	if subID != "" {
		if sub, err := s.store.GetSubscriptionByStripeID(ctx, subID); err == nil {
			return sub, nil
		}
	}
	if customerID != "" {
		if sub, err := s.store.GetSubscriptionByCustomerID(ctx, customerID); err == nil {
			return sub, nil
		}
	}
	return nil, store.ErrNotFound
}
