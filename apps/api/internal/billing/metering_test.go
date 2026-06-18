package billing

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/store"
)

// countingReporter is a UsageReporter test double that tallies every increment it
// is handed. When failNext is set, the NEXT ReportUsage call fails (and clears the
// flag), so a single transient provider error can be simulated.
type countingReporter struct {
	MockProvider
	mu       sync.Mutex
	calls    int
	totalQty int64
	lastQty  int64
	failNext bool
}

func (r *countingReporter) ReportUsage(_ context.Context, _ string, quantity int64, _ time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failNext {
		r.failNext = false
		return errors.New("provider boom")
	}
	r.calls++
	r.lastQty = quantity
	r.totalQty += quantity
	return nil
}

// reportingFixture seeds one org with a metered subscription (si_ item id) and a
// fixed clock, returning the service so a test can drive ReportUsage/ReportAllUsage.
func reportingFixture(t *testing.T, rep PaymentProvider, now time.Time) (*Service, *store.MemoryStore) {
	t.Helper()
	st := store.NewMemoryStore()
	ctx := context.Background()
	if err := st.CreateOrganization(ctx, &domain.Organization{ID: "o1", Slug: "o1"}); err != nil {
		t.Fatalf("org: %v", err)
	}
	if err := st.UpsertSubscription(ctx, &domain.Subscription{
		OrgID: "o1", PlanID: "launch", Status: domain.SubActive,
		StripeSubscriptionID: "sub_live_1", StripeSubscriptionItemID: "si_metered_1",
		CurrentPeriodEnd: now.AddDate(0, 0, 20),
	}); err != nil {
		t.Fatalf("sub: %v", err)
	}
	svc := NewService(st, rep)
	svc.now = func() time.Time { return now }
	return svc, st
}

// addMeteredCents appends one metered compute-cost record (cents -> micro-cents) for
// org o1, anchored inside the current period.
func addMeteredCents(t *testing.T, st store.Store, at time.Time, cents int64) {
	t.Helper()
	if err := st.AddUsage(context.Background(), &domain.UsageRecord{
		ID: meterBucketID("o1", at), OrgID: "o1", Metric: MeterMetric,
		Quantity: cents * 1000, At: at,
	}); err != nil {
		t.Fatalf("add usage: %v", err)
	}
}

// TestReportUsageReportsDeltaOncePerPeriod asserts the idempotency contract: across
// repeated ticks the provider is incremented by exactly the NEW usage each time and
// re-running with no new usage is a no-op — so the period total billed equals the
// period usage and is never double-counted.
func TestReportUsageReportsDeltaOncePerPeriod(t *testing.T) {
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	rep := &countingReporter{}
	svc, st := reportingFixture(t, rep, now)
	ctx := context.Background()

	// Tick 1: 3c of usage so far -> increment by 3.
	addMeteredCents(t, st, now.Add(-2*time.Hour), 3)
	if pushed, err := svc.ReportUsage(ctx, "o1"); err != nil || !pushed {
		t.Fatalf("tick1 ReportUsage pushed=%v err=%v, want pushed=true nil", pushed, err)
	}
	// Tick 2: no new usage -> no-op (delta 0), provider NOT called again.
	if pushed, err := svc.ReportUsage(ctx, "o1"); err != nil || pushed {
		t.Fatalf("tick2 ReportUsage pushed=%v err=%v, want pushed=false nil", pushed, err)
	}
	// Tick 3: 5c more usage (8c cumulative) -> increment by only the 5c delta.
	addMeteredCents(t, st, now.Add(-1*time.Hour), 5)
	if pushed, err := svc.ReportUsage(ctx, "o1"); err != nil || !pushed {
		t.Fatalf("tick3 ReportUsage pushed=%v err=%v, want pushed=true nil", pushed, err)
	}

	if rep.calls != 2 {
		t.Fatalf("provider increment calls = %d, want 2 (no call when delta is 0)", rep.calls)
	}
	if rep.totalQty != 8 {
		t.Fatalf("total reported = %d, want 8 (3 + 5 deltas, never double-counted)", rep.totalQty)
	}
}

// TestReportUsageProviderErrorRetriesSameDelta asserts resilience: a transient
// provider error does NOT advance the watermark, so the SAME delta is retried on the
// next tick and billed exactly once (never lost, never doubled).
func TestReportUsageProviderErrorRetriesSameDelta(t *testing.T) {
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	rep := &countingReporter{failNext: true}
	svc, st := reportingFixture(t, rep, now)
	ctx := context.Background()
	addMeteredCents(t, st, now.Add(-1*time.Hour), 4)

	// Tick 1: the provider call fails -> error surfaces, nothing billed.
	if pushed, err := svc.ReportUsage(ctx, "o1"); err == nil || pushed {
		t.Fatalf("tick1 want (false, error) on provider failure, got pushed=%v err=%v", pushed, err)
	}
	if rep.totalQty != 0 {
		t.Fatalf("nothing should be billed after a failure, got %d", rep.totalQty)
	}
	// Tick 2: retry succeeds -> the same 4c delta is billed exactly once.
	if pushed, err := svc.ReportUsage(ctx, "o1"); err != nil || !pushed {
		t.Fatalf("tick2 retry want (true, nil), got pushed=%v err=%v", pushed, err)
	}
	if rep.totalQty != 4 {
		t.Fatalf("retry should bill the same 4c once, got %d", rep.totalQty)
	}
}

// TestReportAllUsageContinuesOnError asserts the loop-level contract used by the
// metering tick: one org's provider failure does not abort reporting for the others,
// and the first error is surfaced for observability.
func TestReportAllUsageContinuesOnError(t *testing.T) {
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	rep := &countingReporter{}
	svc, st := reportingFixture(t, rep, now)
	ctx := context.Background()

	// Second org with its own metered subscription + usage.
	if err := st.CreateOrganization(ctx, &domain.Organization{ID: "o2", Slug: "o2"}); err != nil {
		t.Fatalf("org2: %v", err)
	}
	if err := st.UpsertSubscription(ctx, &domain.Subscription{
		OrgID: "o2", PlanID: "launch", Status: domain.SubActive,
		StripeSubscriptionID: "sub_live_2", StripeSubscriptionItemID: "si_metered_2",
		CurrentPeriodEnd: now.AddDate(0, 0, 20),
	}); err != nil {
		t.Fatalf("sub2: %v", err)
	}
	addMeteredCents(t, st, now.Add(-1*time.Hour), 3) // o1
	if err := st.AddUsage(ctx, &domain.UsageRecord{
		ID: meterBucketID("o2", now.Add(-1*time.Hour)), OrgID: "o2",
		Metric: MeterMetric, Quantity: 7 * 1000, At: now.Add(-1 * time.Hour),
	}); err != nil {
		t.Fatalf("usage o2: %v", err)
	}

	// Fail exactly one org's report; the other must still be billed.
	rep.failNext = true
	reported, err := svc.ReportAllUsage(ctx)
	if err == nil {
		t.Fatal("ReportAllUsage should surface the first provider error")
	}
	if reported != 1 {
		t.Fatalf("reported orgs = %d, want 1 (the other org despite one failure)", reported)
	}
	if rep.totalQty == 0 {
		t.Fatal("the non-failing org's usage must still be reported")
	}
}

// TestReportAllUsageMockNoOp asserts the dev/test default (MockProvider, no usage
// reporting) makes the whole pass a no-op with no error.
func TestReportAllUsageMockNoOp(t *testing.T) {
	st := store.NewMemoryStore()
	svc := NewService(st, MockProvider{})
	reported, err := svc.ReportAllUsage(context.Background())
	if err != nil || reported != 0 {
		t.Fatalf("mock ReportAllUsage = (%d, %v), want (0, nil)", reported, err)
	}
}

// addUsageFailStore fails the metering write (AddUsageIfAbsent) for one specific
// org, so we can assert that MeterUsage is continue-on-error (one bad org doesn't
// abort the rest of the hour).
type addUsageFailStore struct {
	store.Store
	failOrg string
}

func (s addUsageFailStore) AddUsageIfAbsent(ctx context.Context, u *domain.UsageRecord) (bool, error) {
	if u.OrgID == s.failOrg {
		return false, errors.New("boom")
	}
	return s.Store.AddUsageIfAbsent(ctx, u)
}

func TestMeterUsageContinuesOnError(t *testing.T) {
	mem := store.NewMemoryStore()
	st := addUsageFailStore{Store: mem, failOrg: "bad"}
	ctx := context.Background()

	for _, id := range []string{"bad", "good"} {
		if err := mem.CreateOrganization(ctx, &domain.Organization{ID: id, Slug: id}); err != nil {
			t.Fatalf("create org %s: %v", id, err)
		}
		mustApp(t, mem, &domain.App{ID: "app-" + id, OrgID: id, Name: id, CPU: 4, MemoryMB: 4096, Status: "running", Release: "app-" + id})
	}
	setPrices(t, mem, 0.01, 0.001)

	svc := NewService(st, nil)
	n, err := svc.MeterUsage(ctx)
	if err == nil {
		t.Fatal("expected a non-nil first error from the failing org")
	}
	if n != 1 {
		t.Fatalf("metered orgs = %d, want 1 (the good org despite the bad one)", n)
	}
	recs, _ := mem.ListUsageByOrg(ctx, "good", store.Page{})
	if len(recs) == 0 {
		t.Fatal("expected the good org to be metered despite the bad org failing")
	}
}

// TestMeterUsageConcurrentNoDoubleCount reproduces the TOCTOU: many concurrent
// MeterUsage ticks for the SAME (org, hour) must produce exactly one record. The
// atomic AddUsageIfAbsent makes the per-bucket write race-free (run under -race).
func TestMeterUsageConcurrentNoDoubleCount(t *testing.T) {
	st := store.NewMemoryStore()
	svc := NewService(st, nil)
	meterOrgWithApp(t, st)
	fixed := time.Date(2026, 6, 18, 10, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return fixed }

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = svc.MeterUsage(context.Background())
		}()
	}
	wg.Wait()

	recs, _ := st.ListUsageByOrg(context.Background(), "o1", store.Page{})
	n := 0
	for _, r := range recs {
		if r.Metric == MeterMetric {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("compute records = %d, want 1 (atomic per-hour idempotency under concurrency)", n)
	}
}

// watermarkFailStore fails the metering write for one org only on the FIRST catch-up
// hour, then succeeds, so we can assert the watermark does not advance past an hour
// that did not fully succeed for all orgs.
type watermarkFailStore struct {
	store.Store
	failOrg  string
	failHour time.Time
}

func (s *watermarkFailStore) AddUsageIfAbsent(ctx context.Context, u *domain.UsageRecord) (bool, error) {
	if u.OrgID == s.failOrg && u.At.Equal(s.failHour) {
		return false, errors.New("transient write failure")
	}
	return s.Store.AddUsageIfAbsent(ctx, u)
}

// TestMeterUsageWatermarkStopsAtFailedHour asserts Fix 3: a transient failure on an
// hour must NOT advance the watermark past it, so the next tick retries that hour
// (and the atomic-idempotent write makes the retry safe).
func TestMeterUsageWatermarkStopsAtFailedHour(t *testing.T) {
	mem := store.NewMemoryStore()
	ctx := context.Background()
	if err := mem.CreateOrganization(ctx, &domain.Organization{ID: "o1", Slug: "o1"}); err != nil {
		t.Fatalf("org: %v", err)
	}
	mustApp(t, mem, &domain.App{ID: "a1", OrgID: "o1", Name: "web", CPU: 1, MemoryMB: 1024, Status: "running", Release: "rel"})
	setPrices(t, mem, 0.01, 0.001)

	// First tick at 08:00 establishes the last-metered hour.
	base := NewService(mem, nil)
	base.now = func() time.Time { return time.Date(2026, 6, 18, 8, 0, 0, 0, time.UTC) }
	if _, err := base.MeterUsage(ctx); err != nil {
		t.Fatalf("meter @08: %v", err)
	}

	// Now at 11:00 we must back-fill 09,10,11 — but the write for hour 10 fails.
	failHour := time.Date(2026, 6, 18, 10, 0, 0, 0, time.UTC)
	wf := &watermarkFailStore{Store: mem, failOrg: "o1", failHour: failHour}
	svc := NewService(wf, nil)
	svc.now = func() time.Time { return time.Date(2026, 6, 18, 11, 0, 0, 0, time.UTC) }
	if _, err := svc.MeterUsage(ctx); err == nil {
		t.Fatal("expected the transient write failure to surface")
	}

	// The watermark must have stopped at 09 (the last fully-successful hour before
	// the failure at 10), NOT advanced to 11.
	stt, err := mem.GetMeterState(ctx)
	if err != nil {
		t.Fatalf("meter state: %v", err)
	}
	if got := stt.LastMeteredHour.UTC().Hour(); got != 9 {
		t.Fatalf("watermark hour = %d, want 9 (stopped at the failed hour 10)", got)
	}

	// A retry (no failure now) fills 10 and 11 exactly once, advancing to 11.
	svc2 := NewService(mem, nil)
	svc2.now = func() time.Time { return time.Date(2026, 6, 18, 11, 0, 0, 0, time.UTC) }
	if _, err := svc2.MeterUsage(ctx); err != nil {
		t.Fatalf("retry meter: %v", err)
	}
	recs, _ := mem.ListUsageByOrg(ctx, "o1", store.Page{})
	hours := map[int]int{}
	for _, r := range recs {
		if r.Metric == MeterMetric {
			hours[r.At.UTC().Hour()]++
		}
	}
	for _, h := range []int{8, 9, 10, 11} {
		if hours[h] != 1 {
			t.Fatalf("hour %d metered %d times, want exactly 1; got %v", h, hours[h], hours)
		}
	}
	stt, _ = mem.GetMeterState(ctx)
	if got := stt.LastMeteredHour.UTC().Hour(); got != 11 {
		t.Fatalf("watermark after retry = %d, want 11", got)
	}
}
