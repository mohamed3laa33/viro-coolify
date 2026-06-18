package billing

import (
	"context"
	"testing"
	"time"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/store"
)

// setAllPrices sets cpu/memory/storage/egress hourly prices on the store.
func setAllPrices(t *testing.T, st store.Store, cpu, mem, storage, egress float64) {
	t.Helper()
	ctx := context.Background()
	comps := []domain.PricingComponent{
		{Key: "cpu", Name: "vCPU", Unit: "vCPU-hour", PricePerHour: cpu, Currency: "usd", Active: true, SortOrder: 1},
		{Key: "memory", Name: "Memory", Unit: "GB-hour", PricePerHour: mem, Currency: "usd", Active: true, SortOrder: 2},
		{Key: "storage", Name: "Storage", Unit: "GB-hour", PricePerHour: storage, Currency: "usd", Active: true, SortOrder: 3},
		{Key: "egress", Name: "Egress", Unit: "GB", PricePerHour: egress, Currency: "usd", Active: true, SortOrder: 4},
	}
	for i := range comps {
		if err := st.UpsertPricingComponent(ctx, &comps[i]); err != nil {
			t.Fatalf("set price %s: %v", comps[i].Key, err)
		}
	}
}

// TestMeterUsageMetersStorageDimension asserts the meter writes a SEPARATE storage
// cost bucket (alongside compute) priced from the live "storage" component and the
// database's provisioned StorageGB, and that the two buckets do not collide.
func TestMeterUsageMetersStorageDimension(t *testing.T) {
	st := store.NewMemoryStore()
	svc := NewService(st, nil)
	ctx := context.Background()
	// $0.01/vCPU-hr, free memory, $0.002/GB-hr storage.
	setAllPrices(t, st, 0.01, 0.0, 0.002, 0.0)

	if err := st.CreateOrganization(ctx, &domain.Organization{ID: "o1", Slug: "o1"}); err != nil {
		t.Fatalf("org: %v", err)
	}
	// A billable app (1 vCPU) and a billable database with 50 GB of storage.
	mustApp(t, st, &domain.App{ID: "a1", OrgID: "o1", Name: "web", CPU: 1, MemoryMB: 0, Status: "running", Release: "rel-a1"})
	if err := st.CreateDatabase(ctx, &domain.Database{ID: "d1", OrgID: "o1", Name: "pg", Engine: "postgresql", CPU: 0, MemoryMB: 0, StorageGB: 50, Status: "running", Release: "rel-d1"}); err != nil {
		t.Fatalf("db: %v", err)
	}

	if _, err := svc.MeterUsage(ctx); err != nil {
		t.Fatalf("meter: %v", err)
	}

	recs, _ := st.ListUsageByOrg(ctx, "o1", store.Page{})
	byMetric := map[string]int64{}
	for _, r := range recs {
		byMetric[r.Metric] += r.Quantity
	}
	// Compute: 1 vCPU * $0.01 = $0.01/hr -> 1000 micro-cents.
	if byMetric[MeterMetric] != 1000 {
		t.Fatalf("compute micro-cents = %d, want 1000", byMetric[MeterMetric])
	}
	// Storage: 50 GB * $0.002 = $0.10/hr -> 10000 micro-cents.
	if byMetric[MeterMetricStorage] != 10000 {
		t.Fatalf("storage micro-cents = %d, want 10000", byMetric[MeterMetricStorage])
	}
}

// TestUsageSoFarSumsAllDimensions asserts the invoice usage total sums compute +
// storage + egress (the full cost-to-serve), not compute alone.
func TestUsageSoFarSumsAllDimensions(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Hour)
	recs := []domain.UsageRecord{
		{Metric: MeterMetric, Quantity: 3000, At: now},        // 3c compute
		{Metric: MeterMetricStorage, Quantity: 2000, At: now}, // 2c storage
		{Metric: MeterMetricEgress, Quantity: 1000, At: now},  // 1c egress
		{Metric: "builds", Quantity: 99999, At: now},          // non-cost metric, ignored
	}
	if got := usageSoFarCents(recs); got != 6 {
		t.Fatalf("usageSoFarCents = %d, want 6 (3+2+1)", got)
	}
	dims := usageByDimensionCents(recs)
	if dims[MeterMetric] != 3 || dims[MeterMetricStorage] != 2 || dims[MeterMetricEgress] != 1 {
		t.Fatalf("per-dimension = %v, want compute 3 / storage 2 / egress 1", dims)
	}
	if _, ok := dims["builds"]; ok {
		t.Fatal("non-cost metric must not appear as a cost dimension")
	}
}

// TestRecordEgressGBHonest asserts egress is recorded only from REAL reported GB at
// the live price, and is a no-op when unpriced or zero (never fabricated).
func TestRecordEgressGBHonest(t *testing.T) {
	st := store.NewMemoryStore()
	svc := NewService(st, nil)
	ctx := context.Background()

	// No egress price set yet -> recording is a no-op (no invented cost).
	setAllPrices(t, st, 0.01, 0.0, 0.0, 0.0)
	if err := svc.RecordEgressGB(ctx, "o1", 100); err != nil {
		t.Fatalf("record egress (unpriced): %v", err)
	}
	recs, _ := st.ListUsageByOrg(ctx, "o1", store.Page{})
	if len(recs) != 0 {
		t.Fatalf("unpriced egress must record nothing, got %d records", len(recs))
	}

	// Price egress at $0.05/GB; recording 100 GB -> $5.00 -> 500c -> 500000 micro-cents.
	setAllPrices(t, st, 0.01, 0.0, 0.0, 0.05)
	if err := svc.RecordEgressGB(ctx, "o1", 100); err != nil {
		t.Fatalf("record egress: %v", err)
	}
	// Zero GB is a no-op.
	if err := svc.RecordEgressGB(ctx, "o1", 0); err != nil {
		t.Fatalf("record zero egress: %v", err)
	}
	recs, _ = st.ListUsageByOrg(ctx, "o1", store.Page{})
	var micro int64
	for _, r := range recs {
		if r.Metric == MeterMetricEgress {
			micro += r.Quantity
		}
	}
	if micro != 500000 {
		t.Fatalf("egress micro-cents = %d, want 500000", micro)
	}
}

// TestInvoiceHistoryBucketsByPeriod asserts invoice history buckets usage into the
// subscription's monthly periods, newest first, and stops at empty pre-history.
func TestInvoiceHistoryBucketsByPeriod(t *testing.T) {
	st := store.NewMemoryStore()
	svc := NewService(st, MockProvider{})
	ctx := context.Background()
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }

	if err := st.UpsertPlan(ctx, &domain.Plan{
		ID: "tiny", Name: "Tiny", PriceCents: 1000, Currency: "usd",
		IncludedHours: 0, OveragePerHourCents: 1, MaxCPU: 4, MaxMemoryMB: 8192, MaxApps: 10, Active: true,
	}); err != nil {
		t.Fatalf("plan: %v", err)
	}
	// Current period ends in 10 days, so it started ~20 days ago; the prior period is
	// the month before that.
	periodEnd := now.AddDate(0, 0, 10)
	if err := st.UpsertSubscription(ctx, &domain.Subscription{
		OrgID: "o1", PlanID: "tiny", Status: domain.SubActive, CurrentPeriodEnd: periodEnd,
	}); err != nil {
		t.Fatalf("sub: %v", err)
	}

	// 5c compute in the CURRENT period, 7c compute in the PRIOR period.
	curAt := now.Add(-24 * time.Hour)
	priorAt := periodEnd.AddDate(0, -1, 0).Add(-24 * time.Hour) // before current period start
	if err := st.AddUsage(ctx, &domain.UsageRecord{ID: "cur", OrgID: "o1", Metric: MeterMetric, Quantity: 5000, At: curAt}); err != nil {
		t.Fatalf("usage cur: %v", err)
	}
	if err := st.AddUsage(ctx, &domain.UsageRecord{ID: "prior", OrgID: "o1", Metric: MeterMetric, Quantity: 7000, At: priorAt}); err != nil {
		t.Fatalf("usage prior: %v", err)
	}

	invs, err := svc.InvoiceHistory(ctx, "o1", 6)
	if err != nil {
		t.Fatalf("invoice history: %v", err)
	}
	if len(invs) < 2 {
		t.Fatalf("expected at least 2 periods, got %d", len(invs))
	}
	// Newest first: current period is open with 5c usage.
	if invs[0].Status != InvoiceOpen {
		t.Fatalf("current period status = %q, want open", invs[0].Status)
	}
	if invs[0].UsageSoFarCents != 5 {
		t.Fatalf("current usage = %d, want 5", invs[0].UsageSoFarCents)
	}
	// Prior period is paid with 7c usage.
	if invs[1].Status != InvoicePaid {
		t.Fatalf("prior period status = %q, want paid", invs[1].Status)
	}
	if invs[1].UsageSoFarCents != 7 {
		t.Fatalf("prior usage = %d, want 7", invs[1].UsageSoFarCents)
	}
	// Line items: base (1000c) + compute usage row sum to the charge.
	var sum int64
	for _, li := range invs[0].LineItems {
		sum += li.AmountCents
	}
	if sum != invs[0].ChargeCents {
		t.Fatalf("line items sum %d != charge %d", sum, invs[0].ChargeCents)
	}
}

// TestInvoiceHistoryPastDueStatus asserts a closed period of a past_due org is
// flagged past_due (unsettled), not paid.
func TestInvoiceHistoryPastDueStatus(t *testing.T) {
	st := store.NewMemoryStore()
	svc := NewService(st, MockProvider{})
	ctx := context.Background()
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	periodEnd := now.AddDate(0, 0, 10)
	if err := st.UpsertSubscription(ctx, &domain.Subscription{
		OrgID: "o1", PlanID: "hobby", Status: domain.SubPastDue, CurrentPeriodEnd: periodEnd,
	}); err != nil {
		t.Fatalf("sub: %v", err)
	}
	priorAt := periodEnd.AddDate(0, -1, 0).Add(-24 * time.Hour)
	if err := st.AddUsage(ctx, &domain.UsageRecord{ID: "prior", OrgID: "o1", Metric: MeterMetric, Quantity: 7000, At: priorAt}); err != nil {
		t.Fatalf("usage: %v", err)
	}
	invs, err := svc.InvoiceHistory(ctx, "o1", 3)
	if err != nil {
		t.Fatalf("invoice history: %v", err)
	}
	if len(invs) < 2 || invs[1].Status != InvoicePastDue {
		t.Fatalf("prior period status = %v, want past_due", invs)
	}
}

// TestDunningStatusAndTransitions exercises dunning derivation and the
// advance/clear transitions.
func TestDunningStatusAndTransitions(t *testing.T) {
	st := store.NewMemoryStore()
	svc := NewService(st, MockProvider{})
	ctx := context.Background()

	// No subscription -> current, not blocked.
	if d, err := svc.DunningStatus(ctx, "none"); err != nil || d.PastDue || d.Blocked {
		t.Fatalf("no-sub dunning = %+v err=%v, want current", d, err)
	}

	if err := st.UpsertSubscription(ctx, &domain.Subscription{OrgID: "o1", PlanID: "launch", Status: domain.SubActive, CurrentPeriodEnd: time.Now().AddDate(0, 1, 0)}); err != nil {
		t.Fatalf("sub: %v", err)
	}

	// Payment fails -> past_due. Without grace, blocked.
	if err := svc.AdvanceDunning(ctx, "o1", false); err != nil {
		t.Fatalf("advance: %v", err)
	}
	d, err := svc.DunningStatus(ctx, "o1")
	if err != nil {
		t.Fatalf("dunning: %v", err)
	}
	if !d.PastDue || !d.Blocked || d.State != domain.SubPastDue {
		t.Fatalf("after advance = %+v, want past_due + blocked", d)
	}

	// Admin grace window -> past_due but NOT blocked.
	set, _ := st.GetSettings(ctx)
	set.GracePastDue = true
	if err := st.UpdateSettings(ctx, set); err != nil {
		t.Fatalf("settings: %v", err)
	}
	d, _ = svc.DunningStatus(ctx, "o1")
	if !d.PastDue || d.Blocked || !d.GraceActive {
		t.Fatalf("with grace = %+v, want past_due + grace + not blocked", d)
	}

	// Escalate past_due -> unpaid (always blocked regardless of grace).
	if err := svc.AdvanceDunning(ctx, "o1", true); err != nil {
		t.Fatalf("escalate: %v", err)
	}
	if got, _ := st.GetSubscription(ctx, "o1"); got.Status != domain.SubUnpaid {
		t.Fatalf("escalated status = %q, want unpaid", got.Status)
	}

	// Successful payment clears dunning back to active.
	changed, err := svc.ClearDunning(ctx, "o1")
	if err != nil || !changed {
		t.Fatalf("clear dunning changed=%v err=%v, want true nil", changed, err)
	}
	if got, _ := st.GetSubscription(ctx, "o1"); got.Status != domain.SubActive {
		t.Fatalf("cleared status = %q, want active", got.Status)
	}
}

// TestWebhookInvoicePaymentDrivesDunning asserts the invoice payment events move an
// org into dunning (past_due) and back to active, resolved via the stored customer
// id, and that they are idempotently deduped by event id.
func TestWebhookInvoicePaymentDrivesDunning(t *testing.T) {
	st := store.NewMemoryStore()
	svc := NewService(st, MockProvider{})
	ctx := context.Background()
	subWithID(t, st, "org-1", "sub_abc")

	// Payment failed -> past_due (resolved by customer id, no metadata).
	if processed, err := svc.ProcessEvent(ctx, StripeEvent{
		ID: "evt_fail_1", Type: "invoice.payment_failed",
		Customer: "cus_org-1", Subscription: "sub_abc",
	}); err != nil || !processed {
		t.Fatalf("payment_failed processed=%v err=%v", processed, err)
	}
	if got, _ := st.GetSubscription(ctx, "org-1"); got.Status != domain.SubPastDue {
		t.Fatalf("after payment_failed status = %q, want past_due", got.Status)
	}

	// Redeliver the SAME failed event -> deduped, no change.
	if processed, err := svc.ProcessEvent(ctx, StripeEvent{
		ID: "evt_fail_1", Type: "invoice.payment_failed", Customer: "cus_org-1",
	}); err != nil || processed {
		t.Fatalf("redelivered payment_failed processed=%v err=%v, want deduped", processed, err)
	}

	// Payment succeeded -> back to active.
	if processed, err := svc.ProcessEvent(ctx, StripeEvent{
		ID: "evt_ok_1", Type: "invoice.payment_succeeded",
		Customer: "cus_org-1", Subscription: "sub_abc",
	}); err != nil || !processed {
		t.Fatalf("payment_succeeded processed=%v err=%v", processed, err)
	}
	if got, _ := st.GetSubscription(ctx, "org-1"); got.Status != domain.SubActive {
		t.Fatalf("after payment_succeeded status = %q, want active", got.Status)
	}
}

// TestAdvanceDunningTerminalIsNoOp asserts a canceled subscription is never
// resurrected by a dunning transition.
func TestAdvanceDunningTerminalIsNoOp(t *testing.T) {
	st := store.NewMemoryStore()
	svc := NewService(st, MockProvider{})
	ctx := context.Background()
	if err := st.UpsertSubscription(ctx, &domain.Subscription{OrgID: "o1", PlanID: "launch", Status: domain.SubCanceled, CurrentPeriodEnd: time.Now()}); err != nil {
		t.Fatalf("sub: %v", err)
	}
	if err := svc.AdvanceDunning(ctx, "o1", true); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if got, _ := st.GetSubscription(ctx, "o1"); got.Status != domain.SubCanceled {
		t.Fatalf("canceled must stay canceled, got %q", got.Status)
	}
	if changed, _ := svc.ClearDunning(ctx, "o1"); changed {
		t.Fatal("clearing dunning on a canceled sub must be a no-op")
	}
}
