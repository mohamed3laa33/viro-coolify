package billing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/store"
)

// --- 1. Subscribe linkage: org metadata + stored customer/sub ids ---

func TestSubscribeStoresCustomerAndRealSubID(t *testing.T) {
	st := store.NewMemoryStore()
	svc := NewService(st, MockProvider{})
	ctx := context.Background()

	res, err := svc.Subscribe(ctx, "org-1", "launch", "owner@example.com")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if res.Subscription.StripeCustomerID != "cus_mock_org-1" {
		t.Fatalf("customer id = %q", res.Subscription.StripeCustomerID)
	}
	// The stored subscription id must be a real sub_ id, never a checkout-session id.
	if res.Subscription.StripeSubscriptionID != "sub_mock_org-1_launch" {
		t.Fatalf("subscription id = %q", res.Subscription.StripeSubscriptionID)
	}
	// And it must be resolvable by its stripe sub id (the webhook mapping path).
	got, err := st.GetSubscriptionByStripeID(ctx, "sub_mock_org-1_launch")
	if err != nil || got.OrgID != "org-1" {
		t.Fatalf("lookup by stripe sub id: %v / %+v", err, got)
	}
}

// metaProvider is a Stripe-like provider that records the orgID it was asked to
// attach, and returns an empty sub id + checkout URL (the real Stripe flow).
type metaProvider struct{ gotOrg string }

func (*metaProvider) Name() string { return "meta" }
func (*metaProvider) EnsureCustomer(_ context.Context, orgID, _ string) (string, error) {
	return "cus_" + orgID, nil
}
func (p *metaProvider) CreateSubscription(_ context.Context, orgID, _ string, _ domain.Plan) (ProviderSubscription, error) {
	p.gotOrg = orgID
	return ProviderSubscription{ID: "", Status: string(domain.SubIncomplete), CheckoutURL: "https://checkout"}, nil
}

func TestSubscribeAttachesOrgMetadataAndDefersSubID(t *testing.T) {
	st := store.NewMemoryStore()
	p := &metaProvider{}
	svc := NewService(st, p)
	ctx := context.Background()

	res, err := svc.Subscribe(ctx, "org-7", "launch", "o@e.com")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if p.gotOrg != "org-7" {
		t.Fatalf("provider was not handed org id for metadata: %q", p.gotOrg)
	}
	if res.Subscription.StripeSubscriptionID != "" {
		t.Fatalf("sub id should be empty until checkout completes, got %q", res.Subscription.StripeSubscriptionID)
	}
	if res.CheckoutURL != "https://checkout" {
		t.Fatalf("checkout url = %q", res.CheckoutURL)
	}
}

// --- 2. Webhook: real status mapping (not forced active) + idempotency ---

func subWithID(t *testing.T, st store.Store, orgID, subID string) {
	t.Helper()
	if err := st.UpsertSubscription(context.Background(), &domain.Subscription{
		OrgID: orgID, PlanID: "launch", Status: domain.SubActive,
		StripeCustomerID: "cus_" + orgID, StripeSubscriptionID: subID,
		CurrentPeriodEnd: time.Now().AddDate(0, 1, 0),
	}); err != nil {
		t.Fatalf("seed sub: %v", err)
	}
}

func TestWebhookMapsRealStatusNotForcedActive(t *testing.T) {
	cases := []struct {
		evtType string
		status  string
		want    domain.SubscriptionStatus
	}{
		{"customer.subscription.updated", "past_due", domain.SubPastDue},
		{"customer.subscription.updated", "unpaid", domain.SubUnpaid},
		{"customer.subscription.updated", "trialing", domain.SubTrialing},
		{"customer.subscription.deleted", "canceled", domain.SubCanceled},
		{"customer.subscription.deleted", "active", domain.SubCanceled}, // deleted always cancels
	}
	for _, tc := range cases {
		st := store.NewMemoryStore()
		svc := NewService(st, MockProvider{})
		ctx := context.Background()
		subWithID(t, st, "org-1", "sub_abc")

		processed, err := svc.ProcessEvent(ctx, StripeEvent{
			ID: "evt_" + tc.status + tc.evtType, Type: tc.evtType,
			ID2: "sub_abc", Customer: "cus_org-1", Status: tc.status,
			MetadataOrgID: "org-1",
		})
		if err != nil || !processed {
			t.Fatalf("%s/%s: processed=%v err=%v", tc.evtType, tc.status, processed, err)
		}
		got, _ := st.GetSubscription(ctx, "org-1")
		if got.Status != tc.want {
			t.Fatalf("%s/%s: status = %q want %q", tc.evtType, tc.status, got.Status, tc.want)
		}
	}
}

func TestWebhookCheckoutCompletedCapturesSubID(t *testing.T) {
	st := store.NewMemoryStore()
	svc := NewService(st, MockProvider{})
	ctx := context.Background()
	// Pre-checkout subscription: customer known, sub id empty (Stripe flow).
	// Subscribe seeds a placeholder CurrentPeriodEnd one month out.
	placeholder := time.Now().AddDate(0, 1, 0).UTC()
	if err := st.UpsertSubscription(ctx, &domain.Subscription{
		OrgID: "org-9", PlanID: "launch", Status: domain.SubIncomplete,
		StripeCustomerID: "cus_org-9", CurrentPeriodEnd: placeholder,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// checkout.session.completed: a Checkout SESSION carries no current_period_end;
	// its `status` ("complete") is a SESSION status, not a subscription status.
	processed, err := svc.ProcessEvent(ctx, StripeEvent{
		ID: "evt_cs", Type: "checkout.session.completed",
		Subscription: "sub_real_123", Customer: "cus_org-9",
		ClientReference: "org-9", Status: "complete",
	})
	if err != nil || !processed {
		t.Fatalf("processed=%v err=%v", processed, err)
	}
	got, _ := st.GetSubscription(ctx, "org-9")
	if got.StripeSubscriptionID != "sub_real_123" {
		t.Fatalf("sub id not captured: %q", got.StripeSubscriptionID)
	}
	// A completed checkout must activate (NOT fall to incomplete via the session status).
	if got.Status != domain.SubActive {
		t.Fatalf("status = %q want active (session status must not map to incomplete)", got.Status)
	}
	// Checkout does not move the period end; the placeholder stands until a real
	// customer.subscription.* event arrives.
	if !got.CurrentPeriodEnd.Equal(placeholder) {
		t.Fatalf("checkout must keep the placeholder period end, got %v want %v", got.CurrentPeriodEnd, placeholder)
	}

	// The real subscription event arrives with the period end under items.data[]
	// (Stripe 2025-03+) and the metered si_ item id.
	end := time.Now().Add(720 * time.Hour).Unix()
	if _, err := svc.ProcessEvent(ctx, StripeEvent{
		ID: "evt_sub_created", Type: "customer.subscription.created",
		ID2: "sub_real_123", Customer: "cus_org-9", Status: "active",
		MetadataOrgID: "org-9", ItemCurrentPeriodEnd: end, SubscriptionItemID: "si_metered_1",
	}); err != nil {
		t.Fatalf("subscription.created: %v", err)
	}
	got, _ = st.GetSubscription(ctx, "org-9")
	if got.CurrentPeriodEnd.Unix() != end {
		t.Fatalf("period end from items.data[] not persisted: %v", got.CurrentPeriodEnd)
	}
	if got.StripeSubscriptionItemID != "si_metered_1" {
		t.Fatalf("metered subscription item id not captured: %q", got.StripeSubscriptionItemID)
	}
}

func TestWebhookIdempotentByEventID(t *testing.T) {
	st := store.NewMemoryStore()
	svc := NewService(st, MockProvider{})
	ctx := context.Background()
	subWithID(t, st, "org-1", "sub_abc")

	evt := StripeEvent{
		ID: "evt_dup", Type: "customer.subscription.updated",
		ID2: "sub_abc", Status: "past_due", MetadataOrgID: "org-1",
	}
	p1, err := svc.ProcessEvent(ctx, evt)
	if err != nil || !p1 {
		t.Fatalf("first: processed=%v err=%v", p1, err)
	}
	// Re-deliver the SAME event id: must be a no-op (processed=false), not reapplied.
	p2, err := svc.ProcessEvent(ctx, evt)
	if err != nil {
		t.Fatalf("second: err=%v", err)
	}
	if p2 {
		t.Fatal("redelivered event must be deduped (processed=false)")
	}
	got, _ := st.GetSubscription(ctx, "org-1")
	if got.Status != domain.SubPastDue {
		t.Fatalf("status = %q want past_due", got.Status)
	}
}

// markFailStore fails MarkEventProcessed so we can assert ProcessEvent returns the
// error (-> 5xx, Stripe retries) instead of swallowing it.
type markFailStore struct {
	store.Store
}

func (markFailStore) MarkEventProcessed(context.Context, string) (bool, error) {
	return false, errors.New("db down")
}

func TestWebhookStoreFailureSurfaces(t *testing.T) {
	svc := NewService(markFailStore{Store: store.NewMemoryStore()}, MockProvider{})
	_, err := svc.ProcessEvent(context.Background(), StripeEvent{ID: "evt_x", Type: "customer.subscription.updated"})
	if err == nil {
		t.Fatal("expected a store failure to surface for Stripe retry")
	}
}

// applyFailStore makes the APPLY step (UpsertSubscription) fail, so we can assert
// the BLOCKER fix: ProcessEvent must NOT mark the event processed when apply fails,
// so Stripe's retry re-applies it (no permanent drop).
type applyFailStore struct {
	store.Store
	marked map[string]bool
}

func (s *applyFailStore) UpsertSubscription(context.Context, *domain.Subscription) error {
	return errors.New("apply failed (db down mid-write)")
}

func (s *applyFailStore) MarkEventProcessed(_ context.Context, eventID string) (bool, error) {
	if s.marked == nil {
		s.marked = map[string]bool{}
	}
	s.marked[eventID] = true
	return true, nil
}

func TestWebhookApplyFailureDoesNotMarkProcessed(t *testing.T) {
	mem := store.NewMemoryStore()
	subWithID(t, mem, "org-1", "sub_abc")
	st := &applyFailStore{Store: mem}
	svc := NewService(st, MockProvider{})
	ctx := context.Background()

	evt := StripeEvent{
		ID: "evt_apply_fail", Type: "customer.subscription.updated",
		ID2: "sub_abc", Status: "past_due", MetadataOrgID: "org-1",
	}
	processed, err := svc.ProcessEvent(ctx, evt)
	if err == nil {
		t.Fatal("expected apply failure to surface so Stripe retries")
	}
	if processed {
		t.Fatal("apply failed: event must NOT be reported processed")
	}
	if st.marked["evt_apply_fail"] {
		t.Fatal("BLOCKER: event was marked processed despite a failed apply — retry would drop it")
	}
}

// --- 3. Metering idempotency + catch-up ---

func meterOrgWithApp(t *testing.T, st store.Store) {
	t.Helper()
	ctx := context.Background()
	if err := st.CreateOrganization(ctx, &domain.Organization{ID: "o1", Slug: "o1"}); err != nil {
		t.Fatalf("org: %v", err)
	}
	mustApp(t, st, &domain.App{ID: "a1", OrgID: "o1", Name: "web", CPU: 1, MemoryMB: 1024, Status: "deploying", Release: "rel"})
	setPrices(t, st, 0.01, 0.001)
}

func TestMeterUsageIdempotentPerHour(t *testing.T) {
	st := store.NewMemoryStore()
	svc := NewService(st, nil)
	meterOrgWithApp(t, st)
	fixed := time.Date(2026, 6, 18, 10, 30, 0, 0, time.UTC)
	svc.now = func() time.Time { return fixed }

	if _, err := svc.MeterUsage(context.Background()); err != nil {
		t.Fatalf("meter 1: %v", err)
	}
	// Run again for the same hour: must NOT double-count.
	if _, err := svc.MeterUsage(context.Background()); err != nil {
		t.Fatalf("meter 2: %v", err)
	}
	recs, _ := st.ListUsageByOrg(context.Background(), "o1", store.Page{})
	n := 0
	for _, r := range recs {
		if r.Metric == MeterMetric {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("compute records = %d, want 1 (idempotent per hour)", n)
	}
}

func TestMeterUsageCatchesUpMissedHours(t *testing.T) {
	st := store.NewMemoryStore()
	svc := NewService(st, nil)
	meterOrgWithApp(t, st)
	ctx := context.Background()

	// First tick at 08:00 establishes the last-metered hour.
	svc.now = func() time.Time { return time.Date(2026, 6, 18, 8, 0, 0, 0, time.UTC) }
	if _, err := svc.MeterUsage(ctx); err != nil {
		t.Fatalf("meter @08: %v", err)
	}
	// Process was down until 11:00: a single tick must back-fill 09, 10, 11.
	svc.now = func() time.Time { return time.Date(2026, 6, 18, 11, 0, 0, 0, time.UTC) }
	if _, err := svc.MeterUsage(ctx); err != nil {
		t.Fatalf("meter @11: %v", err)
	}
	recs, _ := st.ListUsageByOrg(ctx, "o1", store.Page{})
	hours := map[string]bool{}
	for _, r := range recs {
		if r.Metric == MeterMetric {
			hours[r.At.UTC().Format("15")] = true
		}
	}
	for _, h := range []string{"08", "09", "10", "11"} {
		if !hours[h] {
			t.Fatalf("missing metered hour %s; got %v", h, hours)
		}
	}
	if len(hours) != 4 {
		t.Fatalf("metered hours = %d, want 4 (exactly once each)", len(hours))
	}
}

// --- 4. Invoice math (base + size-aware overage over the included allowance) ---

func TestInvoiceBasePlusOverage(t *testing.T) {
	st := store.NewMemoryStore()
	svc := NewService(st, MockProvider{})
	ctx := context.Background()
	// Plan whose included allowance = IncludedHours * OveragePerHourCents = 2 * 5 =
	// 10 cents, so usage-so-far beyond 10c is overage, cent-for-cent.
	if err := st.UpsertPlan(ctx, &domain.Plan{
		ID: "tiny", Name: "Tiny", PriceCents: 1000, Currency: "usd",
		IncludedHours: 2, OveragePerHourCents: 5, MaxCPU: 4, MaxMemoryMB: 8192, MaxApps: 10, Active: true,
	}); err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := st.UpsertSubscription(ctx, &domain.Subscription{
		OrgID: "o1", PlanID: "tiny", Status: domain.SubActive,
		CurrentPeriodEnd: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("sub: %v", err)
	}
	// 25 metered compute-hours @ 1100 microcents each = 27500 microcents = 27 cents
	// of size-aware usage. Included allowance = 10c => overage = 27 - 10 = 17c.
	now := time.Now().UTC().Truncate(time.Hour)
	for i := 0; i < 25; i++ {
		if err := st.AddUsage(ctx, &domain.UsageRecord{
			ID:    meterBucketID("o1", now.Add(time.Duration(i)*time.Hour)),
			OrgID: "o1", Metric: MeterMetric, Quantity: 1100, At: now.Add(time.Duration(i) * time.Hour),
		}); err != nil {
			t.Fatalf("usage: %v", err)
		}
	}
	inv, err := svc.CurrentInvoice(ctx, "o1")
	if err != nil {
		t.Fatalf("invoice: %v", err)
	}
	if inv.BaseCents != 1000 {
		t.Fatalf("base = %d want 1000", inv.BaseCents)
	}
	// 25 * 1100 microcents = 27500 microcents = 27 cents usage so far.
	if inv.UsageSoFarCents != 27 {
		t.Fatalf("usage so far = %d want 27", inv.UsageSoFarCents)
	}
	if inv.OverageCents != 17 {
		t.Fatalf("overage = %d want 17 (27c usage - 10c allowance)", inv.OverageCents)
	}
	if inv.ChargeCents != 1017 {
		t.Fatalf("charge = %d want 1017", inv.ChargeCents)
	}
}

// TestInvoiceIsResourceSizeAware locks the core property of the model: usage is
// metered in cents by the live per-component hourly price, so an org running a
// 64-vCPU workload is charged ~64x an org running a 1-vCPU workload for the SAME
// number of hours. The metered records carry the size-priced cost directly, and
// overage (hence the charge) is driven purely by that size-aware usage cost.
func TestInvoiceIsResourceSizeAware(t *testing.T) {
	st := store.NewMemoryStore()
	svc := NewService(st, MockProvider{})
	ctx := context.Background()
	// $0.01/vCPU-hr, free memory: a workload's hourly cost scales linearly with vCPU.
	setPrices(t, st, 0.01, 0.0)
	// A plan with NO included allowance and 1c/hr overage rate (allowance = 0c), so
	// the whole size-aware usage cost flows straight into overage for both orgs.
	if err := st.UpsertPlan(ctx, &domain.Plan{
		ID: "byo", Name: "BYO", PriceCents: 0, Currency: "usd",
		IncludedHours: 0, OveragePerHourCents: 1, MaxCPU: 128, MaxMemoryMB: 1 << 20, MaxApps: 10, Active: true,
	}); err != nil {
		t.Fatalf("plan: %v", err)
	}

	// Two orgs, identical 10 metered hours, but one is 1 vCPU and one is 64 vCPU.
	for _, o := range []struct {
		id  string
		cpu float64
	}{{"small", 1}, {"big", 64}} {
		if err := st.UpsertSubscription(ctx, &domain.Subscription{
			OrgID: o.id, PlanID: "byo", Status: domain.SubActive,
			CurrentPeriodEnd: time.Now().Add(time.Hour),
		}); err != nil {
			t.Fatalf("sub %s: %v", o.id, err)
		}
		mustApp(t, st, &domain.App{ID: "app-" + o.id, OrgID: o.id, Name: o.id, CPU: o.cpu, MemoryMB: 0, Status: "running", Release: "rel-" + o.id})
		now := time.Now().UTC().Truncate(time.Hour)
		for i := 0; i < 10; i++ {
			hour := now.Add(time.Duration(i) * time.Hour)
			// Meter at the live size-aware hourly cost: cpu * $0.01 -> microcents.
			micro := int64(o.cpu * 0.01 * 100.0 * 1000.0)
			if err := st.AddUsage(ctx, &domain.UsageRecord{
				ID: meterBucketID(o.id, hour), OrgID: o.id, Metric: MeterMetric, Quantity: micro, At: hour,
			}); err != nil {
				t.Fatalf("usage %s: %v", o.id, err)
			}
		}
	}

	small, err := svc.CurrentInvoice(ctx, "small")
	if err != nil {
		t.Fatalf("invoice small: %v", err)
	}
	big, err := svc.CurrentInvoice(ctx, "big")
	if err != nil {
		t.Fatalf("invoice big: %v", err)
	}
	if small.ChargeCents <= 0 {
		t.Fatalf("small charge = %d, want > 0", small.ChargeCents)
	}
	// 64-vCPU org pays exactly 64x the 1-vCPU org for the same hours (size-aware).
	if big.ChargeCents != small.ChargeCents*64 {
		t.Fatalf("size-aware billing broken: big charge %d, want 64x small charge %d (=%d)",
			big.ChargeCents, small.ChargeCents, small.ChargeCents*64)
	}
}

// TestEnsureActiveSpendCapPaidPlan locks the spend-cap semantics on a PAID plan
// (non-zero base + non-zero overage): the cap is checked against the SINGLE
// ChargeCents number (base + size-aware overage), with NO double-counting of
// usage-so-far on top.
func TestEnsureActiveSpendCapPaidPlan(t *testing.T) {
	st := store.NewMemoryStore()
	svc := NewService(st, MockProvider{})
	ctx := context.Background()
	// Paid plan: base 1000c, allowance = 10h * 2c = 20c.
	if err := st.UpsertPlan(ctx, &domain.Plan{
		ID: "paid", Name: "Paid", PriceCents: 1000, Currency: "usd",
		IncludedHours: 10, OveragePerHourCents: 2, MaxCPU: 8, MaxMemoryMB: 16384, MaxApps: 50, Active: true,
	}); err != nil {
		t.Fatalf("plan: %v", err)
	}
	// Cap at 1100c. period started a month ago so the records below are in-period.
	if err := st.CreateOrganization(ctx, &domain.Organization{ID: "o1", Slug: "o1", SpendCapCents: 1100}); err != nil {
		t.Fatalf("org: %v", err)
	}
	if err := st.UpsertSubscription(ctx, &domain.Subscription{
		OrgID: "o1", PlanID: "paid", Status: domain.SubActive, CurrentPeriodEnd: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("sub: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Hour)
	addUsage := func(n int, microPerHour int64) {
		for i := 0; i < n; i++ {
			hour := now.Add(time.Duration(i) * time.Hour)
			_ = st.AddUsage(ctx, &domain.UsageRecord{
				ID: meterBucketID("o1", hour), OrgID: "o1", Metric: MeterMetric, Quantity: microPerHour, At: hour,
			})
		}
	}
	// 50c of usage: allowance 20c => overage 30c => charge 1000 + 30 = 1030 < 1100.
	// (If usage-so-far were double-counted, 1030 + 50 = 1080 — still under here, so
	// we deliberately keep the under-cap case unambiguous.)
	addUsage(50, 1000) // 50 records * 1000 microcents = 50000 microcents = 50c
	if err := svc.EnsureActive(ctx, "o1"); err != nil {
		t.Fatalf("under cap (charge 1030 < 1100) should be allowed, got %v", err)
	}
	// Push usage to 100c total: overage = 100 - 20 = 80c => charge = 1080 < 1100,
	// STILL allowed — proving the cap is ChargeCents only. A double-count
	// (1080 + 100 = 1180 >= 1100) would wrongly block here.
	addUsage(50, 1000) // +50c => 100c usage so far, charge 1080
	if err := svc.EnsureActive(ctx, "o1"); err != nil {
		t.Fatalf("charge 1080 < cap 1100 must be allowed (no usage double-count), got %v", err)
	}
	// Push charge to/over the cap: usage 130c => overage 110c => charge 1110 >= 1100.
	addUsage(30, 1000) // +30c => 130c usage so far, charge 1110
	if err := svc.EnsureActive(ctx, "o1"); !errors.Is(err, ErrPaymentRequired) {
		t.Fatalf("charge 1110 >= cap 1100 must block with ErrPaymentRequired, got %v", err)
	}
}

// --- 5. GetBilling current-period usage (not lifetime) ---

func TestGetBillingCurrentPeriodOnly(t *testing.T) {
	st := store.NewMemoryStore()
	svc := NewService(st, MockProvider{})
	ctx := context.Background()
	now := time.Now().UTC()
	svc.now = func() time.Time { return now }
	if err := st.UpsertSubscription(ctx, &domain.Subscription{
		OrgID: "o1", PlanID: "launch", Status: domain.SubActive,
		CurrentPeriodEnd: now.Add(24 * time.Hour), // period started ~29 days ago
	}); err != nil {
		t.Fatalf("sub: %v", err)
	}
	// One usage record inside the period and one well before it (last period).
	inPeriod := now.Add(-24 * time.Hour)
	old := now.AddDate(0, -2, 0)
	_ = st.AddUsage(ctx, &domain.UsageRecord{ID: "u1", OrgID: "o1", Metric: "builds", Quantity: 3, At: inPeriod})
	_ = st.AddUsage(ctx, &domain.UsageRecord{ID: "u2", OrgID: "o1", Metric: "builds", Quantity: 99, At: old})

	sum, err := svc.GetBilling(ctx, "o1")
	if err != nil {
		t.Fatalf("get billing: %v", err)
	}
	if sum.Usage["builds"] != 3 {
		t.Fatalf("current-period builds usage = %d want 3 (old record excluded)", sum.Usage["builds"])
	}
}

// --- EnsureActive gating + spend cap ---

func TestEnsureActiveGating(t *testing.T) {
	cases := []struct {
		status  domain.SubscriptionStatus
		blocked bool
	}{
		{domain.SubActive, false},
		{domain.SubTrialing, false},
		{domain.SubIncomplete, false},
		{domain.SubPastDue, true}, // no grace by default
		{domain.SubUnpaid, true},
		{domain.SubCanceled, true},
	}
	for _, tc := range cases {
		st := store.NewMemoryStore()
		svc := NewService(st, MockProvider{})
		ctx := context.Background()
		_ = st.UpsertSubscription(ctx, &domain.Subscription{OrgID: "o1", PlanID: "launch", Status: tc.status, CurrentPeriodEnd: time.Now().AddDate(0, 1, 0)})

		err := svc.EnsureActive(ctx, "o1")
		if tc.blocked && !errors.Is(err, ErrPaymentRequired) {
			t.Fatalf("%s: expected ErrPaymentRequired, got %v", tc.status, err)
		}
		if !tc.blocked && err != nil {
			t.Fatalf("%s: expected allowed, got %v", tc.status, err)
		}
	}
}

func TestEnsureActiveNoSubscriptionAllowed(t *testing.T) {
	st := store.NewMemoryStore()
	svc := NewService(st, MockProvider{})
	if err := svc.EnsureActive(context.Background(), "org-none"); err != nil {
		t.Fatalf("no subscription should be allowed (free plan), got %v", err)
	}
}

func TestEnsureActivePastDueGrace(t *testing.T) {
	st := store.NewMemoryStore()
	svc := NewService(st, MockProvider{})
	ctx := context.Background()
	// Admin enables the past_due grace window.
	set, _ := st.GetSettings(ctx)
	set.GracePastDue = true
	_ = st.UpdateSettings(ctx, set)
	_ = st.UpsertSubscription(ctx, &domain.Subscription{OrgID: "o1", PlanID: "launch", Status: domain.SubPastDue, CurrentPeriodEnd: time.Now().AddDate(0, 1, 0)})

	if err := svc.EnsureActive(ctx, "o1"); err != nil {
		t.Fatalf("past_due with grace should be allowed, got %v", err)
	}
}

func TestEnsureActiveSpendCapBlocks(t *testing.T) {
	st := store.NewMemoryStore()
	svc := NewService(st, MockProvider{})
	ctx := context.Background()
	_ = st.CreateOrganization(ctx, &domain.Organization{ID: "o1", Slug: "o1", SpendCapCents: 1000})
	// Hobby has a free base (PriceCents 0) and 0 overage, so the cap is driven purely
	// by metered usage-so-far. CurrentPeriodEnd just ahead of now => period started a
	// month ago, comfortably before the metered records below.
	_ = st.UpsertSubscription(ctx, &domain.Subscription{OrgID: "o1", PlanID: "hobby", Status: domain.SubActive, CurrentPeriodEnd: time.Now().Add(time.Hour)})
	if err := svc.EnsureActive(ctx, "o1"); err != nil {
		t.Fatalf("under cap should be allowed, got %v", err)
	}
	// Pile metered usage so far above the 1000c cap (1000 records * 1000 microcents = 1000c).
	now := time.Now().UTC().Truncate(time.Hour)
	for i := 0; i < 1000; i++ {
		_ = st.AddUsage(ctx, &domain.UsageRecord{ID: meterBucketID("o1", now.Add(time.Duration(i)*time.Hour)), OrgID: "o1", Metric: MeterMetric, Quantity: 1000, At: now.Add(time.Duration(i) * time.Hour)})
	}
	if err := svc.EnsureActive(ctx, "o1"); !errors.Is(err, ErrPaymentRequired) {
		t.Fatalf("over cap should block with ErrPaymentRequired, got %v", err)
	}
}

// --- ReportUsage no-op for MockProvider ---

func TestReportUsageMockNoOp(t *testing.T) {
	st := store.NewMemoryStore()
	svc := NewService(st, MockProvider{})
	ctx := context.Background()
	_ = st.UpsertSubscription(ctx, &domain.Subscription{OrgID: "o1", PlanID: "launch", Status: domain.SubActive, StripeSubscriptionID: "sub_1", CurrentPeriodEnd: time.Now().AddDate(0, 1, 0)})
	if _, err := svc.ReportUsage(ctx, "o1"); err != nil {
		t.Fatalf("mock ReportUsage should be a no-op, got %v", err)
	}
}

// fakeUsageReporter is a provider that records the id + quantity handed to
// ReportUsage, so we can assert the si_ item id (not the sub_ id) is reported.
type fakeUsageReporter struct {
	MockProvider
	gotID    string
	gotQty   int64
	gotCalls int
}

func (f *fakeUsageReporter) ReportUsage(_ context.Context, subscriptionItemID string, quantity int64, _ time.Time) error {
	f.gotCalls++
	f.gotID = subscriptionItemID
	f.gotQty = quantity
	return nil
}

func TestReportUsageUsesSubscriptionItemID(t *testing.T) {
	st := store.NewMemoryStore()
	rep := &fakeUsageReporter{}
	svc := NewService(st, rep)
	ctx := context.Background()
	// Fixed clock; place the period end 20 days out so the records below (anchored a
	// few hours back from now) are comfortably inside the current period.
	fixedNow := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return fixedNow }

	// Subscription carries BOTH a sub_ id and a metered si_ item id.
	_ = st.UpsertSubscription(ctx, &domain.Subscription{
		OrgID: "o1", PlanID: "launch", Status: domain.SubActive,
		StripeSubscriptionID: "sub_live_1", StripeSubscriptionItemID: "si_metered_1",
		CurrentPeriodEnd: fixedNow.AddDate(0, 0, 20),
	})
	// 3c of size-aware metered usage this period.
	base := fixedNow.Add(-3 * time.Hour)
	for i := 0; i < 3; i++ {
		at := base.Add(time.Duration(i) * time.Hour)
		_ = st.AddUsage(ctx, &domain.UsageRecord{
			ID: meterBucketID("o1", at), OrgID: "o1",
			Metric: MeterMetric, Quantity: 1000, At: at,
		})
	}

	if _, err := svc.ReportUsage(ctx, "o1"); err != nil {
		t.Fatalf("report usage: %v", err)
	}
	if rep.gotCalls != 1 {
		t.Fatalf("ReportUsage calls = %d, want 1", rep.gotCalls)
	}
	if rep.gotID != "si_metered_1" {
		t.Fatalf("reported id = %q, want the si_ item id (not the sub_ id)", rep.gotID)
	}
	if rep.gotQty != 3 {
		t.Fatalf("reported quantity = %d, want 3 (size-aware usage cents)", rep.gotQty)
	}
}

func TestReportUsageNoOpWithoutItemID(t *testing.T) {
	st := store.NewMemoryStore()
	rep := &fakeUsageReporter{}
	svc := NewService(st, rep)
	ctx := context.Background()
	// Only the sub_ id is known (no si_ yet): reporting must be a no-op, not a 404.
	_ = st.UpsertSubscription(ctx, &domain.Subscription{
		OrgID: "o1", PlanID: "launch", Status: domain.SubActive,
		StripeSubscriptionID: "sub_live_1", CurrentPeriodEnd: time.Now().AddDate(0, 1, 0),
	})
	if _, err := svc.ReportUsage(ctx, "o1"); err != nil {
		t.Fatalf("report usage: %v", err)
	}
	if rep.gotCalls != 0 {
		t.Fatal("ReportUsage must not call the provider without a metered si_ item id")
	}
}
