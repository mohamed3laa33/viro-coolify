package billing

import (
	"context"
	"math"
	"testing"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/store"
)

// setPrices sets admin cpu/memory hourly prices on the store.
func setPrices(t *testing.T, st store.Store, cpu, mem float64) {
	t.Helper()
	ctx := context.Background()
	if err := st.UpsertPricingComponent(ctx, &domain.PricingComponent{Key: "cpu", Name: "vCPU", Unit: "vCPU-hour", PricePerHour: cpu, Currency: "usd", Active: true, SortOrder: 1}); err != nil {
		t.Fatalf("set cpu price: %v", err)
	}
	if err := st.UpsertPricingComponent(ctx, &domain.PricingComponent{Key: "memory", Name: "Memory", Unit: "GB-hour", PricePerHour: mem, Currency: "usd", Active: true, SortOrder: 2}); err != nil {
		t.Fatalf("set memory price: %v", err)
	}
}

func TestSeededPricesAreNonZero(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	svc := NewService(st, nil)
	// Setup-cliff fix: a fresh deploy ships sensible NON-ZERO default rates so it
	// bills something instead of silently running for free. The seed must price a
	// non-trivial workload above zero.
	got, err := svc.HourlyCost(ctx, 4, 8192)
	if err != nil {
		t.Fatalf("HourlyCost: %v", err)
	}
	if got <= 0 {
		t.Fatalf("expected non-zero seeded cost (setup-cliff fix), got %v", got)
	}
	comps, err := svc.PricingComponents(ctx)
	if err != nil {
		t.Fatalf("PricingComponents: %v", err)
	}
	if len(comps) == 0 {
		t.Fatal("expected seeded pricing components (cpu/memory/storage)")
	}

	// Prices remain admin-driven (invariant #1): an admin can override any seeded
	// rate, including back to zero (free).
	setPrices(t, st, 0, 0)
	if got, err := svc.HourlyCost(ctx, 4, 8192); err != nil || got != 0 {
		t.Fatalf("admin zeroing prices should make cost free, got %v (err=%v)", got, err)
	}
}

func TestHourlyAndMonthlyCost(t *testing.T) {
	st := store.NewMemoryStore()
	svc := NewService(st, nil)
	setPrices(t, st, 0.01, 0.001) // $0.01/vCPU-hr, $0.001/GB-hr
	ctx := context.Background()

	// 1 vCPU + 1024MB (1 GB) -> 0.01 + 0.001 = 0.011 / hour.
	got, err := svc.HourlyCost(ctx, 1, 1024)
	if err != nil {
		t.Fatalf("HourlyCost: %v", err)
	}
	if math.Abs(got-0.011) > 1e-9 {
		t.Fatalf("hourly cost = %v, want 0.011", got)
	}
	// Monthly = 0.011 * 730 * 100 = 803 cents.
	gotM, err := svc.MonthlyCostCents(ctx, 1, 1024)
	if err != nil {
		t.Fatalf("MonthlyCostCents: %v", err)
	}
	if gotM != 803 {
		t.Fatalf("monthly cents = %d, want 803", gotM)
	}
}

func TestOrgEstimateAndMetering(t *testing.T) {
	st := store.NewMemoryStore()
	svc := NewService(st, nil)
	setPrices(t, st, 0.01, 0.001)
	ctx := context.Background()

	if err := st.CreateOrganization(ctx, &domain.Organization{ID: "o1", Name: "Acme", Slug: "acme"}); err != nil {
		t.Fatalf("create org: %v", err)
	}
	// A deployed (billable) app and a stopped one (not billed) + a queued one.
	mustApp(t, st, &domain.App{ID: "a1", OrgID: "o1", Name: "web", CPU: 1, MemoryMB: 1024, Status: "deploying", Release: "rel-a1"})
	mustApp(t, st, &domain.App{ID: "a2", OrgID: "o1", Name: "stopped", CPU: 4, MemoryMB: 4096, Status: "stopped", Release: "rel-a2"})
	mustApp(t, st, &domain.App{ID: "a3", OrgID: "o1", Name: "queued", CPU: 4, MemoryMB: 4096, Status: "queued"})

	// Only a1 is billable: 0.011/hr -> 803 cents/month.
	est, err := svc.OrgMonthlyEstimateCents(ctx, "o1")
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}
	if est != 803 {
		t.Fatalf("org monthly estimate = %d, want 803", est)
	}

	// Metering records one hour of cost (in micro-cents) for the org.
	n, err := svc.MeterUsage(ctx)
	if err != nil {
		t.Fatalf("meter: %v", err)
	}
	if n != 1 {
		t.Fatalf("metered orgs = %d, want 1", n)
	}
	recs, _ := st.ListUsageByOrg(ctx, "o1", store.Page{})
	var micro int64
	for _, r := range recs {
		if r.Metric == MeterMetric {
			micro += r.Quantity
		}
	}
	// 0.011 currency/hr -> 0.011*100*1000 = 1100 micro-cents.
	if micro != 1100 {
		t.Fatalf("metered micro-cents = %d, want 1100", micro)
	}
}

func mustApp(t *testing.T, st store.Store, a *domain.App) {
	t.Helper()
	if err := st.CreateApp(context.Background(), a); err != nil {
		t.Fatalf("create app %s: %v", a.ID, err)
	}
}
