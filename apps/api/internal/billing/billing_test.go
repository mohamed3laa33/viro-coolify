package billing

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/store"
)

func TestPlanByID(t *testing.T) {
	svc := NewService(store.NewMemoryStore(), MockProvider{})
	ctx := context.Background()
	if _, ok, err := svc.PlanByID(ctx, "launch"); err != nil || !ok {
		t.Fatalf("expected launch plan in catalog (ok=%v err=%v)", ok, err)
	}
	if _, ok, err := svc.PlanByID(ctx, "nope"); err != nil || ok {
		t.Fatalf("did not expect unknown plan (ok=%v err=%v)", ok, err)
	}
}

func TestCatalogActiveSorted(t *testing.T) {
	svc := NewService(store.NewMemoryStore(), MockProvider{})
	plans, err := svc.Catalog(context.Background())
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	if len(plans) != 3 {
		t.Fatalf("expected 3 active plans, got %d", len(plans))
	}
	for i := 1; i < len(plans); i++ {
		if plans[i-1].SortOrder > plans[i].SortOrder {
			t.Fatalf("plans not sorted by SortOrder: %+v", plans)
		}
	}
}

func TestPlanLimitsFallsBackToDefault(t *testing.T) {
	svc := NewService(store.NewMemoryStore(), MockProvider{})
	ctx := context.Background()
	if lim := svc.PlanLimits(ctx, "hobby"); lim.MaxCPU != 0.5 || lim.MaxMemoryMB != 512 || lim.MaxApps != 3 {
		t.Fatalf("hobby limits = %+v", lim)
	}
	// Unknown plan falls back to the default (hobby) plan's limits.
	if lim := svc.PlanLimits(ctx, "nope"); lim.MaxCPU != 0.5 {
		t.Fatalf("fallback limits = %+v", lim)
	}
}

// catalogFailStore makes the catalog/pricing reads fail, so we can assert the
// billing service PROPAGATES a transient store error instead of swallowing it as
// an empty catalog / zero price (which would render the public endpoints a
// misleading 200 {data:null}).
type catalogFailStore struct {
	store.Store
	err error
}

func (s catalogFailStore) ListPlans(context.Context) ([]domain.Plan, error) {
	return nil, s.err
}
func (s catalogFailStore) GetPlan(context.Context, string) (*domain.Plan, error) {
	return nil, s.err
}
func (s catalogFailStore) ListPricingComponents(context.Context) ([]domain.PricingComponent, error) {
	return nil, s.err
}

func TestCatalogPropagatesStoreError(t *testing.T) {
	boom := errors.New("transient db failure")
	svc := NewService(catalogFailStore{Store: store.NewMemoryStore(), err: boom}, MockProvider{})
	ctx := context.Background()

	if _, err := svc.Catalog(ctx); !errors.Is(err, boom) {
		t.Fatalf("Catalog err = %v, want %v", err, boom)
	}
	if _, _, err := svc.PlanByID(ctx, "launch"); !errors.Is(err, boom) {
		t.Fatalf("PlanByID err = %v, want %v", err, boom)
	}
	if _, err := svc.PricingComponents(ctx); !errors.Is(err, boom) {
		t.Fatalf("PricingComponents err = %v, want %v", err, boom)
	}
	if _, err := svc.HourlyCost(ctx, 1, 1024); !errors.Is(err, boom) {
		t.Fatalf("HourlyCost err = %v, want %v", err, boom)
	}
}

func TestSubscribeAndGetBilling(t *testing.T) {
	svc := NewService(store.NewMemoryStore(), MockProvider{})
	ctx := context.Background()

	res, err := svc.Subscribe(ctx, "org-1", "launch", "owner@example.com")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if res.Subscription.Status != domain.SubActive {
		t.Fatalf("status = %q, want active", res.Subscription.Status)
	}
	if res.Subscription.StripeCustomerID != "cus_mock_org-1" {
		t.Fatalf("customer id = %q", res.Subscription.StripeCustomerID)
	}

	if err := svc.RecordUsage(ctx, "org-1", "compute_hours", 5); err != nil {
		t.Fatalf("usage: %v", err)
	}
	if err := svc.RecordUsage(ctx, "org-1", "compute_hours", 3); err != nil {
		t.Fatalf("usage: %v", err)
	}

	sum, err := svc.GetBilling(ctx, "org-1")
	if err != nil {
		t.Fatalf("get billing: %v", err)
	}
	if sum.Plan == nil || sum.Plan.ID != "launch" {
		t.Fatalf("plan = %+v", sum.Plan)
	}
	if sum.Usage["compute_hours"] != 8 {
		t.Fatalf("usage sum = %d, want 8", sum.Usage["compute_hours"])
	}
}

func TestSubscribeUnknownPlan(t *testing.T) {
	svc := NewService(store.NewMemoryStore(), MockProvider{})
	if _, err := svc.Subscribe(context.Background(), "org-1", "enterprise", "x@y.z"); !errors.Is(err, ErrUnknownPlan) {
		t.Fatalf("expected ErrUnknownPlan, got %v", err)
	}
}

func TestGetBillingNoSubscription(t *testing.T) {
	svc := NewService(store.NewMemoryStore(), nil) // nil -> MockProvider
	sum, err := svc.GetBilling(context.Background(), "org-x")
	if err != nil {
		t.Fatalf("get billing: %v", err)
	}
	if sum.Subscription != nil {
		t.Fatalf("expected no subscription, got %+v", sum.Subscription)
	}
}

func TestVerifyWebhookSignature(t *testing.T) {
	secret := "whsec_test"
	payload := []byte(`{"type":"customer.subscription.updated"}`)
	ts := "1700000000"

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "."))
	mac.Write(payload)
	sig := hex.EncodeToString(mac.Sum(nil))
	header := "t=" + ts + ",v1=" + sig

	// Valid signature (tolerance 0 disables the freshness check).
	if err := VerifyWebhookSignature(payload, header, secret, 0, time.Now()); err != nil {
		t.Fatalf("expected valid signature, got %v", err)
	}
	// Tampered payload.
	if err := VerifyWebhookSignature([]byte(`{"type":"evil"}`), header, secret, 0, time.Now()); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("expected ErrInvalidSignature for tampered payload, got %v", err)
	}
	// Wrong secret.
	if err := VerifyWebhookSignature(payload, header, "whsec_other", 0, time.Now()); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("expected ErrInvalidSignature for wrong secret, got %v", err)
	}
	// Missing secret.
	if err := VerifyWebhookSignature(payload, header, "", 0, time.Now()); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("expected ErrInvalidSignature for empty secret, got %v", err)
	}
}
