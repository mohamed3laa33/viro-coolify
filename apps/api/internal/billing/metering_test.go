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
	recs, _ := mem.ListUsageByOrg(ctx, "good")
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

	recs, _ := st.ListUsageByOrg(context.Background(), "o1")
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
	recs, _ := mem.ListUsageByOrg(ctx, "o1")
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
