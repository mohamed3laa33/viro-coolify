package billing

import (
	"context"
	"errors"
	"time"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/store"
)

// InvoiceStatus is the lifecycle state of a single period invoice.
type InvoiceStatus string

const (
	// InvoiceOpen is the current, still-accruing billing period: its usage and
	// charge keep growing until the period closes.
	InvoiceOpen InvoiceStatus = "open"
	// InvoicePaid is a closed past period for an org in good standing (active /
	// trialing / no subscription). The period total is final.
	InvoicePaid InvoiceStatus = "paid"
	// InvoicePastDue is a closed past period whose subscription is in dunning
	// (past_due / unpaid / canceled): the charge is final but unsettled.
	InvoicePastDue InvoiceStatus = "past_due"
)

// InvoiceLineItem is one priced row of a period invoice. Line items break the
// charge into the plan base, the metered cost dimensions (compute/storage/egress)
// and, when it applies, the overage adjustment. All amounts are whole cents in the
// invoice currency. Quantities/prices are NOT re-derived here — the line items are
// computed from the period's actual metered usage records and the store-backed plan
// (invariant #1: no hardcoded business values).
type InvoiceLineItem struct {
	// Key identifies the row: "base" (plan fee), a cost dimension metric
	// (compute_cost_microcents/storage_cost_microcents/egress_cost_microcents) or
	// "included_allowance" (the negative usage allowance bundled into the plan).
	Key string `json:"key"`
	// Label is a human-readable description for the dashboard/CLI.
	Label string `json:"label"`
	// AmountCents is the line amount (can be negative for the included allowance).
	AmountCents int64 `json:"amountCents"`
}

// PeriodInvoice is the computed invoice for one billing period. There is no
// separate persisted invoice entity: it is derived deterministically from the
// store-backed plan, the org's subscription period boundaries and the period's
// metered usage records — the single source of truth (invariant #1). This keeps
// invoice history truthful (it always reflects the real metered usage) and avoids a
// parallel persistence path.
type PeriodInvoice struct {
	PeriodStart time.Time     `json:"periodStart"`
	PeriodEnd   time.Time     `json:"periodEnd"`
	Status      InvoiceStatus `json:"status"`
	Currency    string        `json:"currency"`

	BaseCents       int64 `json:"baseCents"`
	OverageCents    int64 `json:"overageCents"`
	UsageSoFarCents int64 `json:"usageSoFarCents"`
	ChargeCents     int64 `json:"chargeCents"`

	// LineItems break the charge down (base + per-dimension usage + allowance).
	LineItems []InvoiceLineItem `json:"lineItems"`
}

// maxInvoicePeriods bounds how many historical periods InvoiceHistory will compute
// in one call, so a long-lived org cannot force an unbounded month-by-month scan.
const maxInvoicePeriods = 24

// InvoiceHistory returns up to limit most-recent billing periods for an org
// (newest first), starting with the current OPEN period and walking back one
// month at a time over closed periods. Each period invoice is computed from the
// plan + that period's metered usage records, so the history reflects the real
// size-aware cost-to-serve. A non-positive or oversized limit is clamped to a sane
// default/maximum.
//
// Periods are anchored on the subscription's CurrentPeriodEnd (one calendar month
// each, matching periodStart); an org with no subscription gets rolling 30-day-ish
// monthly windows anchored on now. Closed periods carry the subscription's dunning
// status (paid vs past_due) so the dashboard can flag unsettled invoices.
func (s *Service) InvoiceHistory(ctx context.Context, orgID string, limit int) ([]PeriodInvoice, error) {
	if limit <= 0 {
		limit = 12
	}
	if limit > maxInvoicePeriods {
		limit = maxInvoicePeriods
	}

	var sub *domain.Subscription
	var plan *domain.Plan
	if got, err := s.store.GetSubscription(ctx, orgID); err == nil {
		sub = got
		p, ok, perr := s.PlanByID(ctx, got.PlanID)
		if perr != nil {
			return nil, perr
		}
		if ok {
			plan = &p
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}

	currency, err := s.pricingCurrency(ctx)
	if err != nil {
		return nil, err
	}

	// Current open period boundaries: [periodStart, periodEnd).
	periodEnd := s.periodEnd(sub)
	out := make([]PeriodInvoice, 0, limit)
	for i := 0; i < limit; i++ {
		start := periodEnd.AddDate(0, -1, 0)
		// Read the full period window once (unbounded page) and slice to [start,end).
		recs, err := s.store.ListUsageByOrgSince(ctx, orgID, start, store.Page{})
		if err != nil {
			return nil, err
		}
		inPeriod := recs[:0:0]
		for _, r := range recs {
			if !r.At.Before(start) && r.At.Before(periodEnd) {
				inPeriod = append(inPeriod, r)
			}
		}
		inv := s.invoiceFromRecords(plan, inPeriod)
		pi := PeriodInvoice{
			PeriodStart:     start,
			PeriodEnd:       periodEnd,
			Currency:        currency,
			Status:          s.invoiceStatus(sub, i == 0),
			BaseCents:       inv.BaseCents,
			OverageCents:    inv.OverageCents,
			UsageSoFarCents: inv.UsageSoFarCents,
			ChargeCents:     inv.ChargeCents,
			LineItems:       lineItems(plan, inPeriod, inv),
		}
		out = append(out, pi)

		// Stop emitting empty pre-history: once we hit a fully-empty closed period
		// (no base, no usage), older periods are necessarily empty too.
		if i > 0 && pi.ChargeCents == 0 && pi.UsageSoFarCents == 0 {
			break
		}
		periodEnd = start
	}
	return out, nil
}

// periodEnd returns the end of the org's current billing period: the subscription's
// CurrentPeriodEnd when set, else one month after periodStart(nil) i.e. now (so an
// unsubscribed org still gets a rolling current window ending now).
func (s *Service) periodEnd(sub *domain.Subscription) time.Time {
	if sub != nil && !sub.CurrentPeriodEnd.IsZero() {
		return sub.CurrentPeriodEnd
	}
	return s.now()
}

// invoiceStatus classifies a period invoice. The current period is always open; a
// closed period inherits the org's dunning standing — past_due/unpaid/canceled
// subscriptions yield InvoicePastDue, everything else (active/trialing/incomplete/
// no subscription) yields InvoicePaid.
func (s *Service) invoiceStatus(sub *domain.Subscription, current bool) InvoiceStatus {
	if current {
		return InvoiceOpen
	}
	if sub != nil {
		switch sub.Status {
		case domain.SubPastDue, domain.SubUnpaid, domain.SubCanceled:
			return InvoicePastDue
		}
	}
	return InvoicePaid
}

// lineItems builds the per-row breakdown of a period invoice: the plan base, the
// included-allowance credit (negative), and one row per non-zero metered cost
// dimension. They are ordered base, allowance, then dimensions by a stable order so
// the dashboard/CLI render deterministically.
func lineItems(plan *domain.Plan, records []domain.UsageRecord, inv Invoice) []InvoiceLineItem {
	items := make([]InvoiceLineItem, 0, 5)
	if inv.BaseCents != 0 {
		items = append(items, InvoiceLineItem{Key: "base", Label: planLabel(plan), AmountCents: inv.BaseCents})
	}
	// Per-dimension usage rows (compute/storage/egress), stable-ordered.
	byDim := usageByDimensionCents(records)
	for _, m := range []string{MeterMetric, MeterMetricStorage, MeterMetricEgress} {
		if c := byDim[m]; c > 0 {
			items = append(items, InvoiceLineItem{Key: m, Label: dimensionLabel(m), AmountCents: c})
		}
	}
	// The included allowance is a credit against usage (shown only when the plan
	// actually bundles one and there is usage to offset), so the line items sum to
	// the charge: base + sum(usage) - min(usage, allowance) == base + overage.
	if plan != nil {
		allowance := int64(plan.IncludedHours) * int64(plan.OveragePerHourCents)
		if credit := minInt64(allowance, inv.UsageSoFarCents); credit > 0 {
			items = append(items, InvoiceLineItem{Key: "included_allowance", Label: "Included usage allowance", AmountCents: -credit})
		}
	}
	return items
}

func planLabel(plan *domain.Plan) string {
	if plan != nil && plan.Name != "" {
		return plan.Name + " plan"
	}
	return "Plan base"
}

// dimensionLabel returns a human-readable label for a metered cost dimension.
func dimensionLabel(metric string) string {
	switch metric {
	case MeterMetric:
		return "Compute (vCPU + memory)"
	case MeterMetricStorage:
		return "Storage"
	case MeterMetricEgress:
		return "Network egress"
	default:
		return metric
	}
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// Dunning is an org's collections standing, derived from its subscription status
// and the admin grace policy (PlatformSettings.GracePastDue) — never hardcoded.
type Dunning struct {
	// State mirrors the subscription status that drives dunning: "current" when in
	// good standing, else the blocking/at-risk status (past_due/unpaid/canceled).
	State domain.SubscriptionStatus `json:"state"`
	// PastDue is true when the org is in any dunning state (past_due/unpaid/canceled).
	PastDue bool `json:"pastDue"`
	// Blocked is true when this standing currently blocks new provisioning/deploys
	// (EnsureActive would return ErrPaymentRequired for billing reasons). A past_due
	// org with the admin grace window enabled is PastDue but NOT blocked.
	Blocked bool `json:"blocked"`
	// GraceActive is true when the org is past_due AND the admin grace window keeps
	// it running. Only meaningful when State == past_due.
	GraceActive bool `json:"graceActive"`
}

// DunningStatus reports the org's collections standing. An org with no subscription
// is "current" (it runs on the free/default plan). The grace policy comes from the
// admin platform settings; a store error is propagated rather than masked as
// "current" so a transient failure never silently clears a real dunning state.
func (s *Service) DunningStatus(ctx context.Context, orgID string) (Dunning, error) {
	sub, err := s.store.GetSubscription(ctx, orgID)
	if errors.Is(err, store.ErrNotFound) {
		return Dunning{State: domain.SubActive}, nil
	}
	if err != nil {
		return Dunning{}, err
	}
	d := Dunning{State: sub.Status}
	switch sub.Status {
	case domain.SubPastDue:
		d.PastDue = true
		d.GraceActive = s.gracePastDue(ctx)
		d.Blocked = !d.GraceActive
	case domain.SubUnpaid, domain.SubCanceled:
		d.PastDue = true
		d.Blocked = true
	}
	return d, nil
}

// AdvanceDunning is the collections transition the platform applies when a payment
// is reported failed for an org (e.g. a Stripe invoice.payment_failed webhook, or
// an admin action). It moves an org's subscription into the dunning state, leaving
// terminal states (canceled) untouched and escalating an already-past_due org to
// unpaid only when escalate is set. It is idempotent: re-applying the same target
// state is a harmless upsert. A no-op (no subscription, or already in/past the
// target state) returns nil. The actual blocking of deploys is enforced separately
// by EnsureActive reading this status — this method only records the state.
//
// Business policy (grace, escalation cadence) stays admin/DB-driven; this method is
// the mechanism, not a hardcoded schedule, so a scheduler/webhook decides WHEN.
func (s *Service) AdvanceDunning(ctx context.Context, orgID string, escalate bool) error {
	sub, err := s.store.GetSubscription(ctx, orgID)
	if errors.Is(err, store.ErrNotFound) {
		return nil // no subscription -> nothing to dun
	}
	if err != nil {
		return err
	}
	switch sub.Status {
	case domain.SubCanceled:
		return nil // terminal; do not resurrect
	case domain.SubUnpaid:
		return nil // already at the escalated dunning state
	case domain.SubPastDue:
		if !escalate {
			return nil // already past_due and not escalating
		}
		sub.Status = domain.SubUnpaid
	default:
		// active/trialing/incomplete -> first dunning step.
		sub.Status = domain.SubPastDue
	}
	return s.store.UpsertSubscription(ctx, sub)
}

// ClearDunning returns an org's subscription to active after a successful payment
// (e.g. invoice.payment_succeeded, or an admin override). It is idempotent and a
// no-op for a canceled subscription (a canceled sub is reactivated only by a real
// new subscription event, never by clearing dunning) or when there is no
// subscription. Returns whether the status actually changed.
func (s *Service) ClearDunning(ctx context.Context, orgID string) (changed bool, err error) {
	sub, err := s.store.GetSubscription(ctx, orgID)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	switch sub.Status {
	case domain.SubPastDue, domain.SubUnpaid:
		sub.Status = domain.SubActive
		if err := s.store.UpsertSubscription(ctx, sub); err != nil {
			return false, err
		}
		return true, nil
	default:
		return false, nil
	}
}
