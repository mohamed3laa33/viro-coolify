package billing

import (
	"context"
	"math"
	"sort"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
)

// MeterMetric is the usage metric under which metered hourly compute cost is
// recorded. Quantities are in micro-cents (1 cent = 1000 micro-cents) to keep
// sub-cent hourly precision; sum and divide by 1000 for cents.
const MeterMetric = "compute_cost_microcents"

// hoursPerMonth is the average number of hours in a month (365*24/12), used to
// convert an hourly rate into a monthly estimate.
const hoursPerMonth = 730.0

// PricingComponents returns the active, admin-managed pricing components sorted by
// SortOrder. Prices are always read live from the store — never hardcoded.
func (s *Service) PricingComponents(ctx context.Context) []domain.PricingComponent {
	comps, err := s.store.ListPricingComponents(ctx)
	if err != nil {
		return nil
	}
	active := make([]domain.PricingComponent, 0, len(comps))
	for _, c := range comps {
		if c.Active {
			active = append(active, c)
		}
	}
	sort.Slice(active, func(i, j int) bool { return active[i].SortOrder < active[j].SortOrder })
	return active
}

// priceMap returns component-key -> price-per-hour for active components.
func (s *Service) priceMap(ctx context.Context) map[string]float64 {
	m := map[string]float64{}
	for _, c := range s.PricingComponents(ctx) {
		m[c.Key] = c.PricePerHour
	}
	return m
}

// pricingCurrency returns the currency of the price list (from the cpu component,
// falling back to usd).
func (s *Service) pricingCurrency(ctx context.Context) string {
	for _, c := range s.PricingComponents(ctx) {
		if c.Currency != "" {
			return c.Currency
		}
	}
	return "usd"
}

// HourlyCost returns the per-hour cost (in the price-list currency, e.g. dollars)
// of a workload of the given size, from the live admin price list. cpu is in
// vCPU, memMB in megabytes.
func (s *Service) HourlyCost(ctx context.Context, cpu float64, memMB int) float64 {
	p := s.priceMap(ctx)
	return cpu*p["cpu"] + (float64(memMB)/1024.0)*p["memory"]
}

// MonthlyCostCents returns the estimated monthly cost (rounded to whole cents) of
// a single workload of the given size at current prices.
func (s *Service) MonthlyCostCents(ctx context.Context, cpu float64, memMB int) int64 {
	return int64(math.Round(s.HourlyCost(ctx, cpu, memMB) * hoursPerMonth * 100.0))
}

// billable reports whether a workload of the given (release, status) currently
// accrues cost: it must be deployed (has a release) and not stopped.
func billable(release, status string) bool {
	return release != "" && status != "stopped"
}

// orgHourlyCost sums the hourly cost of an org's currently-running workloads
// (apps + services + databases) at the live price list.
func (s *Service) orgHourlyCost(ctx context.Context, orgID string) (float64, error) {
	total := 0.0
	apps, err := s.store.ListAppsByOrg(ctx, orgID)
	if err != nil {
		return 0, err
	}
	for _, a := range apps {
		if billable(a.Release, a.Status) {
			total += s.HourlyCost(ctx, a.CPU, a.MemoryMB)
		}
	}
	svcs, err := s.store.ListServicesByOrg(ctx, orgID)
	if err != nil {
		return 0, err
	}
	for _, sv := range svcs {
		if billable(sv.Release, sv.Status) {
			total += s.HourlyCost(ctx, sv.CPU, sv.MemoryMB)
		}
	}
	dbs, err := s.store.ListDatabasesByOrg(ctx, orgID)
	if err != nil {
		return 0, err
	}
	for _, d := range dbs {
		if billable(d.Release, d.Status) {
			total += s.HourlyCost(ctx, d.CPU, d.MemoryMB)
		}
	}
	return total, nil
}

// OrgMonthlyEstimateCents returns the estimated monthly cost (whole cents) of all
// of an org's currently-running workloads at the live price list.
func (s *Service) OrgMonthlyEstimateCents(ctx context.Context, orgID string) (int64, error) {
	hourly, err := s.orgHourlyCost(ctx, orgID)
	if err != nil {
		return 0, err
	}
	return int64(math.Round(hourly * hoursPerMonth * 100.0)), nil
}

// MeterUsage records one hour of compute cost for every org with running
// workloads, at the live price list. It is meant to be invoked once per hour by
// a scheduler. Cost is stored in micro-cents (see MeterMetric) to preserve
// sub-cent precision; orgs with no billable workloads (or zero prices) are
// skipped. Returns the number of orgs metered.
func (s *Service) MeterUsage(ctx context.Context) (int, error) {
	orgs, err := s.store.ListAllOrgs(ctx)
	if err != nil {
		return 0, err
	}
	at := s.now()
	metered := 0
	for _, org := range orgs {
		hourly, err := s.orgHourlyCost(ctx, org.ID)
		if err != nil {
			return metered, err
		}
		microCents := int64(math.Round(hourly * 100.0 * 1000.0)) // currency -> micro-cents
		if microCents <= 0 {
			continue
		}
		rec := &domain.UsageRecord{
			ID:       s.idgen(),
			OrgID:    org.ID,
			Metric:   MeterMetric,
			Quantity: microCents,
			At:       at,
		}
		if err := s.store.AddUsage(ctx, rec); err != nil {
			return metered, err
		}
		metered++
	}
	return metered, nil
}
