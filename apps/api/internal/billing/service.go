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
	Usage        map[string]int64     `json:"usage"`
	// Estimated cost of the org's currently-running workloads at the live admin
	// price list (hourly pricing). Currency matches the price list.
	EstimatedMonthlyCents int64  `json:"estimatedMonthlyCents"`
	Currency              string `json:"currency"`
}

// GetBilling returns the billing summary for an org.
func (s *Service) GetBilling(ctx context.Context, orgID string) (*Summary, error) {
	out := &Summary{Usage: map[string]int64{}}
	sub, err := s.store.GetSubscription(ctx, orgID)
	switch {
	case err == nil:
		out.Subscription = sub
		if p, ok := s.PlanByID(ctx, sub.PlanID); ok {
			out.Plan = &p
		}
	case errors.Is(err, store.ErrNotFound):
		// No subscription yet — that's fine.
	default:
		return nil, err
	}

	records, err := s.store.ListUsageByOrg(ctx, orgID)
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
	out.Currency = s.pricingCurrency(ctx)
	return out, nil
}

// SubscribeResult is the outcome of subscribing an org to a plan.
type SubscribeResult struct {
	Subscription *domain.Subscription `json:"subscription"`
	CheckoutURL  string               `json:"checkoutUrl,omitempty"`
}

// Subscribe subscribes an org to a plan via the payment provider.
func (s *Service) Subscribe(ctx context.Context, orgID, planID, email string) (*SubscribeResult, error) {
	plan, ok := s.PlanByID(ctx, planID)
	if !ok {
		return nil, ErrUnknownPlan
	}
	customerID, err := s.provider.EnsureCustomer(ctx, orgID, email)
	if err != nil {
		return nil, err
	}
	ps, err := s.provider.CreateSubscription(ctx, customerID, plan)
	if err != nil {
		return nil, err
	}
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
