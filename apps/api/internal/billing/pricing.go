package billing

import (
	"context"
	"math"
	"sort"
	"time"

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

// maxCatchupHours bounds how many missed whole hours a single MeterUsage tick will
// back-fill after a downtime gap, so a long outage cannot trigger an unbounded
// metering storm on the first tick back.
const maxCatchupHours = 48

// meterBucketID returns the deterministic usage-record id for one (org, hour)
// bucket, so an atomic insert-if-absent (AddUsageIfAbsent) records each org's
// compute cost at most once per hour. A restart, a second replica or a concurrent
// tick that re-runs the same hour produces the SAME id and is skipped — no
// check-then-act race.
func meterBucketID(orgID string, hour time.Time) string {
	return "meter-" + orgID + "-" + hour.UTC().Format("20060102T15")
}

// MeterUsage meters compute cost for every org with running workloads, one record
// per (org, whole-UTC-hour) bucket, at the live price list. It is meant to be
// invoked roughly once per hour by a scheduler but is SAFE to call more often or
// after a gap:
//
//   - Idempotent per (org, hour): the per-bucket write is ATOMIC
//     (AddUsageIfAbsent, keyed by a deterministic id), so a restart, a second
//     replica or a concurrent tick never double-counts — no check-then-act race.
//   - Catch-up: it meters every missed whole hour strictly after the last metered
//     hour up to the current hour (bounded by maxCatchupHours), filling a downtime
//     gap exactly once.
//   - Watermark safety: the last-metered hour is advanced ONLY over hours that
//     fully succeeded for EVERY org. At the first hour where any org failed (or
//     the context is canceled), advancing stops, so that hour is retried on the
//     next tick. Because the per-bucket write is atomic-idempotent, re-metering an
//     hour whose other orgs already succeeded is harmless.
//
// Cost is stored in micro-cents (see MeterMetric). Orgs with no billable workloads
// (or zero prices) are skipped for that hour. Returns the number of (org, hour)
// records written. It keeps the Wave-1 continue-on-error behavior: one org's
// failure does not abort the rest of that hour, and the first error is returned.
func (s *Service) MeterUsage(ctx context.Context) (int, error) {
	orgs, err := s.store.ListAllOrgs(ctx)
	if err != nil {
		return 0, err
	}

	currentHour := s.now().UTC().Truncate(time.Hour)
	hours := s.hoursToMeter(ctx, currentHour)

	written := 0
	var firstErr error
	// lastDone is the highest hour that fully succeeded for ALL orgs (contiguously
	// from the start). It only advances while every prior hour also fully succeeded,
	// so a failure mid-run never moves the watermark past an unmetered hour.
	lastDone := time.Time{}
	fullyOK := true
	for _, hour := range hours {
		hourOK := true
		for _, org := range orgs {
			// Short-circuit on shutdown so we stop querying the pool once the process
			// is draining (the store may be about to close).
			if err := ctx.Err(); err != nil {
				if firstErr == nil {
					firstErr = err
				}
				return written, s.persistAndReturn(ctx, lastDone, firstErr)
			}
			n, err := s.meterOrgHour(ctx, org.ID, hour)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				hourOK = false // this hour is not complete; do not advance past it
				continue
			}
			written += n
		}
		// Advance the watermark only while every hour so far has fully succeeded. As
		// soon as an hour has any failure, stop advancing so it (and later hours) are
		// retried next tick.
		if fullyOK && hourOK {
			lastDone = hour
		} else {
			fullyOK = false
		}
	}
	return written, s.persistAndReturn(ctx, lastDone, firstErr)
}

// meterOrgHour atomically records one org's compute cost for one whole hour at the
// live price list, returning the number of records written (0 or 1). It is
// idempotent per (org, hour) via AddUsageIfAbsent: a duplicate bucket is a no-op,
// not an error, so retries are safe. Orgs with no billable cost are skipped.
func (s *Service) meterOrgHour(ctx context.Context, orgID string, hour time.Time) (int, error) {
	hourly, err := s.orgHourlyCost(ctx, orgID)
	if err != nil {
		return 0, err
	}
	microCents := int64(math.Round(hourly * 100.0 * 1000.0)) // currency -> micro-cents
	if microCents <= 0 {
		return 0, nil
	}
	inserted, err := s.store.AddUsageIfAbsent(ctx, &domain.UsageRecord{
		ID:       meterBucketID(orgID, hour),
		OrgID:    orgID,
		Metric:   MeterMetric,
		Quantity: microCents,
		At:       hour,
	})
	if err != nil {
		return 0, err
	}
	if inserted {
		return 1, nil
	}
	return 0, nil
}

// hoursToMeter returns the ordered list of whole UTC hours to meter on this tick:
// every hour strictly after the persisted last-metered hour, up to and including
// currentHour, bounded by maxCatchupHours. On the very first run (no state) it
// meters only the current hour.
func (s *Service) hoursToMeter(ctx context.Context, currentHour time.Time) []time.Time {
	last := time.Time{}
	if st, err := s.store.GetMeterState(ctx); err == nil && st != nil {
		last = st.LastMeteredHour.UTC().Truncate(time.Hour)
	}
	if last.IsZero() || !last.Before(currentHour) {
		// First run, or already current: meter just the current hour (unless it was
		// already metered, in which case nothing is appended).
		if last.Equal(currentHour) {
			return nil
		}
		return []time.Time{currentHour}
	}
	var hours []time.Time
	for h := last.Add(time.Hour); !h.After(currentHour); h = h.Add(time.Hour) {
		hours = append(hours, h)
		if len(hours) >= maxCatchupHours {
			break
		}
	}
	return hours
}

// persistAndReturn records the last metered hour (when progress was made) and
// returns the accumulated first error. A failure to persist the meter state is
// surfaced only when no prior error exists, so metering progress isn't masked.
func (s *Service) persistAndReturn(ctx context.Context, lastDone time.Time, firstErr error) error {
	if !lastDone.IsZero() {
		if err := s.store.SetMeterState(ctx, &domain.MeterState{LastMeteredHour: lastDone}); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
