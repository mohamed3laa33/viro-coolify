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

// usageSoFarCents sums the period's metered cost across EVERY priced dimension
// (compute + storage + egress, all in micro-cents) and converts to whole cents.
// This is the single, size-aware source of truth for usage: each record already
// reflects a workload's resource size (or measured egress) at the live rate, so the
// total is the real cost-to-serve for the period.
func usageSoFarCents(records []domain.UsageRecord) int64 {
	var microCents int64
	for _, r := range records {
		if isCostMetric(r.Metric) {
			microCents += r.Quantity
		}
	}
	return microCents / 1000
}

// usageByDimensionCents returns the period cost (whole cents) of each priced
// dimension keyed by metric, so an invoice can present per-line-item amounts. Only
// dimensions with non-zero cost are included. The summed values equal
// usageSoFarCents for the same records.
func usageByDimensionCents(records []domain.UsageRecord) map[string]int64 {
	micro := map[string]int64{}
	for _, r := range records {
		if isCostMetric(r.Metric) {
			micro[r.Metric] += r.Quantity
		}
	}
	out := map[string]int64{}
	for metric, mc := range micro {
		if cents := mc / 1000; cents > 0 {
			out[metric] = cents
		}
	}
	return out
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
//
// quantity is in whole CENTS of size-aware metered compute cost — the SAME unit
// computed by Service.ReportUsage and stored end to end (see usageSoFarCents). It
// is deliberately NOT compute-hours; the provider's metered price must be set so 1
// unit = 1 cent. Keeping a single unit (cents) across meter → store → report
// avoids the cents-vs-hours mismatch.
type UsageReporter interface {
	ReportUsage(ctx context.Context, subscriptionItemID string, quantity int64, at time.Time) error
}

// ReportAllUsage reports the current-period metered usage of EVERY org to the
// payment provider, intended to run once per metering tick right after MeterUsage.
// It mirrors MeterUsage's continue-on-error contract: a single org's reporting
// failure (e.g. a transient provider error) does not abort the rest, and the first
// error is returned for observability. Because reporting is idempotent per org (see
// ReportUsage), a failed org is simply retried — and re-billed for the same cents —
// on the next tick, never double-billed. Returns the number of orgs whose usage was
// actually pushed to the provider (delta > 0).
func (s *Service) ReportAllUsage(ctx context.Context) (int, error) {
	// A provider with no usage reporting (MockProvider in dev/tests) makes the whole
	// pass a no-op — skip the org scan entirely.
	if _, ok := s.provider.(UsageReporter); !ok {
		return 0, nil
	}
	orgs, err := s.store.ListAllOrgs(ctx)
	if err != nil {
		return 0, err
	}
	reported := 0
	var firstErr error
	for _, org := range orgs {
		// Stop promptly on shutdown so we don't keep hitting a draining store/provider.
		if err := ctx.Err(); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			break
		}
		pushed, err := s.ReportUsage(ctx, org.ID)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if pushed {
			reported++
		}
	}
	return reported, firstErr
}

// ReportUsage reports an org's current-period size-aware metered usage (in cents)
// to the payment provider for metered/usage-based billing, IDEMPOTENTLY. It is a
// no-op (pushed=false) when the provider does not support usage reporting
// (MockProvider) or the org has no metered subscription-item id yet. The reported
// id is the si_ item id captured from the subscription webhook (the per-item usage
// endpoint requires it); reporting against the sub_ id would 404 in production.
//
// Idempotency (mirrors the metering loop's per-bucket watermark): Stripe usage
// records use action=increment, so reporting the cumulative period total on every
// tick would DOUBLE-BILL. Instead this reports only the DELTA — the current-period
// cents MINUS the cents already reported for this period (the per-org
// UsageReportState watermark) — and advances the watermark ONLY after the provider
// accepts the increment. A re-run with no new usage therefore reports nothing; a
// provider error leaves the watermark unmoved so the same delta is retried next
// tick (re-billing the same cents exactly once, never twice). When the billing
// period rolls over (the period start moves forward) the watermark resets to zero
// so the new period's usage starts from a clean slate.
//
// pushed reports whether a non-zero increment was actually sent to the provider.
func (s *Service) ReportUsage(ctx context.Context, orgID string) (pushed bool, err error) {
	reporter, ok := s.provider.(UsageReporter)
	if !ok {
		return false, nil // provider has no usage reporting (mock) -> no-op
	}
	sub, err := s.store.GetSubscription(ctx, orgID)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	// Report against the metered subscription-ITEM id (si_), not the sub_ id.
	if sub.StripeSubscriptionItemID == "" {
		return false, nil
	}
	periodStart := s.periodStart(sub)
	records, err := s.store.ListUsageByOrgSince(ctx, orgID, periodStart, store.Page{})
	if err != nil {
		return false, err
	}
	cents := usageSoFarCents(records)

	// Load the per-org reported-usage watermark. A missing watermark (or one from a
	// prior billing period) means nothing has been reported for THIS period yet.
	already := int64(0)
	if rs, rerr := s.store.GetUsageReportState(ctx, orgID); rerr == nil && rs != nil {
		if !rs.PeriodStart.Before(periodStart) {
			already = rs.ReportedCents
		}
	} else if rerr != nil && !errors.Is(rerr, store.ErrNotFound) {
		return false, rerr
	}

	delta := cents - already
	if delta <= 0 {
		// Nothing new to report (already reported, or no/negative usage). Still
		// persist the period anchor so a period rollover is recorded even with zero
		// new usage, keeping the watermark from leaking across periods.
		if err := s.persistReportState(ctx, orgID, periodStart, already); err != nil {
			return false, err
		}
		return false, nil
	}
	if err := reporter.ReportUsage(ctx, sub.StripeSubscriptionItemID, delta, s.now()); err != nil {
		// Provider error: do NOT advance the watermark, so this exact delta is retried
		// next tick (idempotent — the same cents are billed once, never twice).
		return false, err
	}
	if err := s.persistReportState(ctx, orgID, periodStart, cents); err != nil {
		return false, err
	}
	return true, nil
}

// persistReportState advances an org's usage-reporting watermark to (periodStart,
// cents). It is called only after the provider has accepted the increment (or when
// there is nothing to report), so the watermark never runs ahead of what the
// provider actually billed.
func (s *Service) persistReportState(ctx context.Context, orgID string, periodStart time.Time, cents int64) error {
	return s.store.SetUsageReportState(ctx, &domain.UsageReportState{
		OrgID:         orgID,
		PeriodStart:   periodStart,
		ReportedCents: cents,
	})
}
