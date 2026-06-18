package billing

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/store"
)

// ErrUnknownPlan is returned when subscribing to a plan that is not in the catalog.
var ErrUnknownPlan = errors.New("billing: unknown plan")

// ErrPaymentRequired is returned by EnsureActive when an org's subscription does
// not permit new provisioning/deploys (canceled/unpaid/past_due without grace) or
// the org is over its spend cap. The HTTP layer maps it to 402 Payment Required.
var ErrPaymentRequired = errors.New("billing: payment required")

// Service implements plan catalog, subscriptions and usage metering.
type Service struct {
	store    store.Store
	provider PaymentProvider
	idgen    func() string
	now      func() time.Time
}

// NewService builds a billing service. A nil provider defaults to MockProvider.
func NewService(s store.Store, p PaymentProvider) *Service {
	if p == nil {
		p = MockProvider{}
	}
	return &Service{store: s, provider: p, idgen: uuid.NewString, now: time.Now}
}

// ProviderName reports the active payment provider ("mock" or "stripe").
func (s *Service) ProviderName() string { return s.provider.Name() }

// Summary is an org's billing overview: subscription, plan, and usage totals.
type Summary struct {
	Subscription *domain.Subscription `json:"subscription"`
	Plan         *domain.Plan         `json:"plan"`
	// Usage totals are for the CURRENT billing period (since the period start), not
	// lifetime — keyed by metric.
	Usage map[string]int64 `json:"usage"`
	// Estimated cost of the org's currently-running workloads at the live admin
	// price list (hourly pricing). Currency matches the price list.
	EstimatedMonthlyCents int64 `json:"estimatedMonthlyCents"`
	// Current-period charge breakdown (all in the plan currency, whole cents):
	// BaseCents = plan PriceCents; OverageCents = overage beyond included hours;
	// UsageSoFarCents = metered compute cost so far this period; ChargeCents =
	// base + overage (the projected invoice). PeriodStart is the period start.
	BaseCents       int64     `json:"baseCents"`
	OverageCents    int64     `json:"overageCents"`
	UsageSoFarCents int64     `json:"usageSoFarCents"`
	ChargeCents     int64     `json:"chargeCents"`
	PeriodStart     time.Time `json:"periodStart"`
	Currency        string    `json:"currency"`
}

// GetBilling returns the billing summary for an org. Usage totals and the charge
// are scoped to the CURRENT billing period.
func (s *Service) GetBilling(ctx context.Context, orgID string) (*Summary, error) {
	out := &Summary{Usage: map[string]int64{}}
	var sub *domain.Subscription
	got, err := s.store.GetSubscription(ctx, orgID)
	switch {
	case err == nil:
		sub = got
		out.Subscription = got
		p, ok, perr := s.PlanByID(ctx, got.PlanID)
		if perr != nil {
			return nil, perr
		}
		if ok {
			out.Plan = &p
		}
	case errors.Is(err, store.ErrNotFound):
		// No subscription yet — that's fine.
	default:
		return nil, err
	}

	periodStart := s.periodStart(sub)
	out.PeriodStart = periodStart

	records, err := s.store.ListUsageByOrgSince(ctx, orgID, periodStart, store.Page{})
	if err != nil {
		return nil, err
	}
	for _, r := range records {
		out.Usage[r.Metric] += r.Quantity
	}

	// Estimated cost of currently-running workloads at the live price list.
	est, err := s.OrgMonthlyEstimateCents(ctx, orgID)
	if err != nil {
		return nil, err
	}
	out.EstimatedMonthlyCents = est

	// Current-period charge: base plan price + metered-hours overage beyond the
	// plan's included hours.
	inv := s.invoiceFromRecords(out.Plan, records)
	out.BaseCents = inv.BaseCents
	out.OverageCents = inv.OverageCents
	out.UsageSoFarCents = inv.UsageSoFarCents
	out.ChargeCents = inv.ChargeCents
	cur, err := s.pricingCurrency(ctx)
	if err != nil {
		return nil, err
	}
	out.Currency = cur
	return out, nil
}

// periodStart returns the start of the org's current billing period: one month
// before CurrentPeriodEnd when a subscription exists, else one month before now
// (so an unsubscribed org still gets a rolling 30-day window).
func (s *Service) periodStart(sub *domain.Subscription) time.Time {
	if sub != nil && !sub.CurrentPeriodEnd.IsZero() {
		return sub.CurrentPeriodEnd.AddDate(0, -1, 0)
	}
	return s.now().AddDate(0, -1, 0)
}

// SubscribeResult is the outcome of subscribing an org to a plan.
type SubscribeResult struct {
	Subscription *domain.Subscription `json:"subscription"`
	CheckoutURL  string               `json:"checkoutUrl,omitempty"`
}

// Subscribe subscribes an org to a plan via the payment provider.
func (s *Service) Subscribe(ctx context.Context, orgID, planID, email string) (*SubscribeResult, error) {
	plan, ok, err := s.PlanByID(ctx, planID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrUnknownPlan
	}
	customerID, err := s.provider.EnsureCustomer(ctx, orgID, email)
	if err != nil {
		return nil, err
	}
	ps, err := s.provider.CreateSubscription(ctx, orgID, customerID, plan)
	if err != nil {
		return nil, err
	}
	// Store the customer id always. The subscription id is stored only when the
	// provider already knows it (MockProvider, inline activation); for a Stripe
	// checkout flow it stays empty here and is captured from
	// checkout.session.completed — the cs_ session id is never persisted as the
	// subscription id.
	sub := &domain.Subscription{
		OrgID:                orgID,
		PlanID:               plan.ID,
		Status:               domain.SubscriptionStatus(ps.Status),
		StripeCustomerID:     customerID,
		StripeSubscriptionID: ps.ID,
		CreatedAt:            s.now(),
		CurrentPeriodEnd:     s.now().AddDate(0, 1, 0),
	}
	if err := s.store.UpsertSubscription(ctx, sub); err != nil {
		return nil, err
	}
	return &SubscribeResult{Subscription: sub, CheckoutURL: ps.CheckoutURL}, nil
}

// RecordUsage appends a metered usage event for an org.
func (s *Service) RecordUsage(ctx context.Context, orgID, metric string, quantity int64) error {
	return s.store.AddUsage(ctx, &domain.UsageRecord{
		ID:       s.idgen(),
		OrgID:    orgID,
		Metric:   metric,
		Quantity: quantity,
		At:       s.now(),
	})
}

// SetSubscriptionStatus updates an org's subscription status (used by webhooks).
func (s *Service) SetSubscriptionStatus(ctx context.Context, orgID string, status domain.SubscriptionStatus) error {
	sub, err := s.store.GetSubscription(ctx, orgID)
	if err != nil {
		return err
	}
	sub.Status = status
	return s.store.UpsertSubscription(ctx, sub)
}
