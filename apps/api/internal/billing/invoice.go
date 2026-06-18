package billing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/store"
)

// Invoice is the computed current-period charge for an org, all in whole cents of
// the plan currency.
//
// The billing model is ONE coherent, size-aware model whose single source of truth
// for usage is the metered compute cost (compute_cost_microcents), which already
// prices each running workload by its real resource SIZE at the live per-component
// hourly rate. Summing the period's records (microcents/1000) yields
// UsageSoFarCents — the real, resource-size-aware usage cost.
//
//	UsageSoFarCents        = sum(compute_cost_microcents this period) / 1000
//	includedAllowanceCents = Plan.IncludedHours * Plan.OveragePerHourCents
//	OverageCents           = max(0, UsageSoFarCents - includedAllowanceCents)
//	BaseCents              = Plan.PriceCents
//	ChargeCents            = BaseCents + OverageCents
//
// A plan is therefore a base fee (PriceCents) plus an included usage allowance.
// includedAllowanceCents documents the interpretation: "the plan includes a usage
// allowance equal to IncludedHours priced at the overage rate". There is NO
// separate flat record-count overage — overage is driven purely by the size-aware
// metered usage cost, so a 64-vCPU org pays ~64x a 1-vCPU org for the same hours.
type Invoice struct {
	BaseCents       int64
	OverageCents    int64
	UsageSoFarCents int64
	ChargeCents     int64
}

// usageSoFarCents sums the period's metered compute cost (in micro-cents) and
// converts to whole cents. This is the single, size-aware source of truth for
// usage: each record already reflects a workload's resource size at the live rate.
func usageSoFarCents(records []domain.UsageRecord) int64 {
	var microCents int64
	for _, r := range records {
		if r.Metric == MeterMetric {
			microCents += r.Quantity
		}
	}
	return microCents / 1000
}

// invoiceFromRecords computes the current-period invoice from the plan and the
// org's current-period usage records. A nil plan yields a zero invoice (no
// subscription => nothing to bill against a base plan; usage-so-far still shown).
func (s *Service) invoiceFromRecords(plan *domain.Plan, records []domain.UsageRecord) Invoice {
	var inv Invoice
	inv.UsageSoFarCents = usageSoFarCents(records)
	if plan == nil {
		return inv
	}
	inv.BaseCents = int64(plan.PriceCents)

	// The plan's included usage allowance, in cents: IncludedHours priced at the
	// plan's overage rate. Overage is the size-aware usage cost beyond that
	// allowance — never a flat per-record count.
	includedAllowanceCents := int64(plan.IncludedHours) * int64(plan.OveragePerHourCents)
	if over := inv.UsageSoFarCents - includedAllowanceCents; over > 0 {
		inv.OverageCents = over
	}
	inv.ChargeCents = inv.BaseCents + inv.OverageCents
	return inv
}

// CurrentInvoice computes the org's current-period invoice from its subscription
// plan and current-period metered usage.
func (s *Service) CurrentInvoice(ctx context.Context, orgID string) (Invoice, error) {
	var sub *domain.Subscription
	var plan *domain.Plan
	if got, err := s.store.GetSubscription(ctx, orgID); err == nil {
		sub = got
		p, ok, perr := s.PlanByID(ctx, got.PlanID)
		if perr != nil {
			return Invoice{}, perr
		}
		if ok {
			plan = &p
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return Invoice{}, err
	}
	records, err := s.store.ListUsageByOrgSince(ctx, orgID, s.periodStart(sub), store.Page{})
	if err != nil {
		return Invoice{}, err
	}
	return s.invoiceFromRecords(plan, records), nil
}

// EnsureActive gates new provisioning/deploys for an org. It blocks (ErrPaymentRequired)
// when the subscription is canceled or unpaid, when it is past_due and the admin
// policy does not grant a grace window, or when the org's current-period charge
// would exceed its spend cap. An org with no subscription is allowed (it runs on
// the default/free plan). Active/trialing/incomplete are allowed.
func (s *Service) EnsureActive(ctx context.Context, orgID string) error {
	sub, err := s.store.GetSubscription(ctx, orgID)
	if errors.Is(err, store.ErrNotFound) {
		// No subscription: free/default plan — allowed, but still spend-capped.
		return s.ensureUnderCap(ctx, orgID)
	}
	if err != nil {
		return err
	}

	switch sub.Status {
	case domain.SubCanceled, domain.SubUnpaid:
		return errPayment("subscription %s", sub.Status)
	case domain.SubPastDue:
		if !s.gracePastDue(ctx) {
			return errPayment("subscription past_due")
		}
	}
	return s.ensureUnderCap(ctx, orgID)
}

// ensureUnderCap blocks when the org's current-period charge would meet/exceed its
// effective spend cap (per-org SpendCapCents, else the platform default). A zero
// effective cap means "no cap".
//
// The cap is checked against a SINGLE number — ChargeCents (base + overage) — which
// already incorporates the size-aware metered usage via OverageCents. Adding
// UsageSoFarCents on top would double-count the usage, so it is deliberately not
// added. On a free/zero-overage plan ChargeCents collapses to the base, and a cap
// that is purely usage-driven is enforced by an admin setting OveragePerHourCents
// (so usage flows into OverageCents). See SpendCapCents in domain.Organization.
func (s *Service) ensureUnderCap(ctx context.Context, orgID string) error {
	capCents := s.effectiveSpendCap(ctx, orgID)
	if capCents <= 0 {
		return nil
	}
	inv, err := s.CurrentInvoice(ctx, orgID)
	if err != nil {
		return err
	}
	// Committed spend this period IS the projected charge (base + size-aware
	// overage). Do not add UsageSoFarCents — overage already reflects it.
	if inv.ChargeCents >= capCents {
		return errPayment("spend cap reached (%d/%d cents)", inv.ChargeCents, capCents)
	}
	return nil
}

// effectiveSpendCap resolves the org's spend cap: the per-org SpendCapCents when
// set (>0), else the platform DefaultSpendCapCents. All admin/DB-driven.
func (s *Service) effectiveSpendCap(ctx context.Context, orgID string) int64 {
	if org, err := s.store.GetOrganization(ctx, orgID); err == nil && org != nil && org.SpendCapCents > 0 {
		return org.SpendCapCents
	}
	if set, err := s.store.GetSettings(ctx); err == nil && set != nil {
		return set.DefaultSpendCapCents
	}
	return 0
}

// gracePastDue reports the admin policy on whether a past_due org keeps running.
func (s *Service) gracePastDue(ctx context.Context) bool {
	if set, err := s.store.GetSettings(ctx); err == nil && set != nil {
		return set.GracePastDue
	}
	return false
}

func errPayment(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrPaymentRequired}, args...)...)
}

// UsageReporter is implemented by providers that support reporting metered usage
// to the upstream billing system (Stripe usage records). The MockProvider does
// not implement it, so ReportUsage is a no-op in local/dev and tests.
//
// subscriptionItemID is the metered subscription-ITEM id (si_…), NOT the
// subscription id (sub_…): Stripe's usage_records endpoint is per-item and 404s on
// a sub_ id.
type UsageReporter interface {
	ReportUsage(ctx context.Context, subscriptionItemID string, quantity int64, at time.Time) error
}

// ReportUsage reports an org's current-period size-aware metered usage (in cents)
// to the payment provider for metered/usage-based billing. It is a no-op when the
// provider does not support usage reporting (MockProvider) or the org has no
// metered subscription-item id yet. The reported id is the si_ item id captured
// from the subscription webhook (the per-item usage endpoint requires it); reporting
// against the sub_ id would 404 in production.
func (s *Service) ReportUsage(ctx context.Context, orgID string) error {
	reporter, ok := s.provider.(UsageReporter)
	if !ok {
		return nil // provider has no usage reporting (mock) -> no-op
	}
	sub, err := s.store.GetSubscription(ctx, orgID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	// Report against the metered subscription-ITEM id (si_), not the sub_ id.
	if sub.StripeSubscriptionItemID == "" {
		return nil
	}
	records, err := s.store.ListUsageByOrgSince(ctx, orgID, s.periodStart(sub), store.Page{})
	if err != nil {
		return err
	}
	cents := usageSoFarCents(records)
	if cents <= 0 {
		return nil
	}
	return reporter.ReportUsage(ctx, sub.StripeSubscriptionItemID, cents, s.now())
}
