package billing

import (
	"context"
	"math"
	"sort"
	"time"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
)

// Metered cost dimensions. Each is an admin-priced cost-to-serve dimension whose
// per-hour cost is recorded as a separate usage record so an invoice can break the
// charge down by line item while the period total is just their sum. Quantities
// are in micro-cents (1 cent = 1000 micro-cents) to keep sub-cent hourly precision;
// sum and divide by 1000 for cents.
//
//   - MeterMetric (compute): vCPU-hours + GB-hours of every running workload at the
//     live "cpu"/"memory" prices. This is the historical metric and stays the canonical
//     name for backwards compatibility.
//   - MeterMetricStorage (storage): provisioned database GB-hours at the live
//     "storage" price. Storage accrues whenever a database holds a persistent volume
//     (it is billed while provisioned, not only while serving requests).
//   - MeterMetricEgress (egress): network egress GB priced at the live "egress" price.
//     Egress has no synthetic data source — it is recorded ONLY from real reported GB
//     (RecordEgressGB), never fabricated (invariant #6), so absent a metrics pipeline
//     it simply stays zero.
const (
	MeterMetric        = "compute_cost_microcents"
	MeterMetricStorage = "storage_cost_microcents"
	MeterMetricEgress  = "egress_cost_microcents"
)

// costMetrics is the set of metered cost dimensions, all in micro-cents, that sum
// into an org's period usage cost. Adding a dimension here makes it flow into the
// invoice/usage-so-far total automatically.
var costMetrics = map[string]struct{}{
	MeterMetric:        {},
	MeterMetricStorage: {},
	MeterMetricEgress:  {},
}

// isCostMetric reports whether a usage metric is one of the priced cost dimensions
// (compute/storage/egress) that contribute to the period usage cost.
func isCostMetric(metric string) bool {
	_, ok := costMetrics[metric]
	return ok
}

// Pricing-component keys (admin-managed in the store; never hardcoded prices). The
// KEY identifies which component a price belongs to; the PRICE itself is read live
// from the admin price list.
const (
	priceKeyCPU     = "cpu"
	priceKeyMemory  = "memory"
	priceKeyStorage = "storage"
	priceKeyEgress  = "egress"
)

// hoursPerMonth is the average number of hours in a month (365*24/12), used to
// convert an hourly rate into a monthly estimate.
const hoursPerMonth = 730.0

// PricingComponents returns the active, admin-managed pricing components sorted by
// SortOrder. Prices are always read live from the store — never hardcoded. A store
// error is PROPAGATED so the public pricing endpoint returns a 5xx instead of a
// 200 with an empty/null price list (and metering never silently reads zero prices).
func (s *Service) PricingComponents(ctx context.Context) ([]domain.PricingComponent, error) {
	comps, err := s.store.ListPricingComponents(ctx)
	if err != nil {
		return nil, err
	}
	active := make([]domain.PricingComponent, 0, len(comps))
	for _, c := range comps {
		if c.Active {
			active = append(active, c)
		}
	}
	sort.Slice(active, func(i, j int) bool { return active[i].SortOrder < active[j].SortOrder })
	return active, nil
}

// priceMap returns component-key -> price-per-hour for active components.
func (s *Service) priceMap(ctx context.Context) (map[string]float64, error) {
	comps, err := s.PricingComponents(ctx)
	if err != nil {
		return nil, err
	}
	m := map[string]float64{}
	for _, c := range comps {
		m[c.Key] = c.PricePerHour
	}
	return m, nil
}

// pricingCurrency returns the currency of the price list (from the first component
// carrying one, falling back to usd). A store error is propagated.
func (s *Service) pricingCurrency(ctx context.Context) (string, error) {
	comps, err := s.PricingComponents(ctx)
	if err != nil {
		return "", err
	}
	for _, c := range comps {
		if c.Currency != "" {
			return c.Currency, nil
		}
	}
	return "usd", nil
}

// HourlyCost returns the per-hour cost (in the price-list currency, e.g. dollars)
// of a workload of the given size, from the live admin price list. cpu is in
// vCPU, memMB in megabytes. A store error is propagated rather than masked as a
// zero (free) cost.
func (s *Service) HourlyCost(ctx context.Context, cpu float64, memMB int) (float64, error) {
	p, err := s.priceMap(ctx)
	if err != nil {
		return 0, err
	}
	return cpu*p[priceKeyCPU] + (float64(memMB)/1024.0)*p[priceKeyMemory], nil
}

// MonthlyCostCents returns the estimated monthly cost (rounded to whole cents) of
// a single workload of the given size at current prices.
func (s *Service) MonthlyCostCents(ctx context.Context, cpu float64, memMB int) (int64, error) {
	hourly, err := s.HourlyCost(ctx, cpu, memMB)
	if err != nil {
		return 0, err
	}
	return int64(math.Round(hourly * hoursPerMonth * 100.0)), nil
}

// billable reports whether a workload of the given (release, status) currently
// accrues cost: it must be deployed (has a release) and not stopped.
func billable(release, status string) bool {
	return release != "" && status != "stopped"
}

// orgHourlyCost sums the hourly cost of an org's currently-running workloads
// (apps + services + databases) at the live price list.
func (s *Service) orgHourlyCost(ctx context.Context, orgID string) (float64, error) {
	// Resolve the price list once so a transient store error surfaces here instead
	// of being masked into a zero per-workload cost.
	prices, err := s.priceMap(ctx)
	if err != nil {
		return 0, err
	}
	hourly := func(cpu float64, memMB int) float64 {
		return cpu*prices[priceKeyCPU] + (float64(memMB)/1024.0)*prices[priceKeyMemory]
	}
	total := 0.0
	apps, err := s.store.ListAppsByOrg(ctx, orgID)
	if err != nil {
		return 0, err
	}
	for _, a := range apps {
		if billable(a.Release, a.Status) {
			total += hourly(a.CPU, a.MemoryMB)
		}
	}
	svcs, err := s.store.ListServicesByOrg(ctx, orgID)
	if err != nil {
		return 0, err
	}
	for _, sv := range svcs {
		if billable(sv.Release, sv.Status) {
			total += hourly(sv.CPU, sv.MemoryMB)
		}
	}
	dbs, err := s.store.ListDatabasesByOrg(ctx, orgID)
	if err != nil {
		return 0, err
	}
	for _, d := range dbs {
		if billable(d.Release, d.Status) {
			total += hourly(d.CPU, d.MemoryMB)
		}
	}
	return total, nil
}

// orgStorageHourlyCost sums the hourly cost of an org's provisioned persistent
// storage (database volumes) at the live "storage" price. Storage is billed while
// a database is provisioned (deployed and not stopped) — it occupies a volume even
// when idle — so it uses the same billable() gate as compute. A missing/zero
// "storage" price yields zero cost (the platform never invents a price).
func (s *Service) orgStorageHourlyCost(ctx context.Context, orgID string) (float64, error) {
	prices, err := s.priceMap(ctx)
	if err != nil {
		return 0, err
	}
	rate := prices[priceKeyStorage] // price per GB-hour; 0 when unpriced
	if rate <= 0 {
		return 0, nil
	}
	dbs, err := s.store.ListDatabasesByOrg(ctx, orgID)
	if err != nil {
		return 0, err
	}
	total := 0.0
	for _, d := range dbs {
		if d.StorageGB > 0 && billable(d.Release, d.Status) {
			total += float64(d.StorageGB) * rate
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
// COMPUTE bucket, so an atomic insert-if-absent (AddUsageIfAbsent) records each
// org's compute cost at most once per hour. A restart, a second replica or a
// concurrent tick that re-runs the same hour produces the SAME id and is skipped —
// no check-then-act race. The compute id keeps its original (suffix-less) shape for
// backwards compatibility with already-written rows; other dimensions get a
// dimension-suffixed id via dimBucketID.
func meterBucketID(orgID string, hour time.Time) string {
	return "meter-" + orgID + "-" + hour.UTC().Format("20060102T15")
}

// dimBucketID returns the deterministic per-(org, hour, dimension) usage-record id
// for a non-compute cost dimension (storage, …). It is namespaced by metric so the
// storage bucket never collides with the compute bucket for the same (org, hour),
// while remaining idempotent per dimension under AddUsageIfAbsent.
func dimBucketID(orgID, metric string, hour time.Time) string {
	return "meter-" + metric + "-" + orgID + "-" + hour.UTC().Format("20060102T15")
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

// meterOrgHour atomically records one org's cost-to-serve for one whole hour at the
// live price list, across every metered dimension (compute + storage), returning
// the number of records written (0..N). Each dimension is a separate, idempotent
// per-(org, hour, dimension) record via AddUsageIfAbsent: a duplicate bucket is a
// no-op, not an error, so retries are safe. Dimensions with no billable cost are
// skipped. If ANY dimension write fails the error is returned (and the caller does
// not advance the watermark past this hour); a partially-written hour is harmless
// because each bucket is atomic-idempotent and re-tried next tick.
func (s *Service) meterOrgHour(ctx context.Context, orgID string, hour time.Time) (int, error) {
	written := 0

	// Compute (vCPU-hours + GB-hours). Keeps the original (suffix-less) bucket id.
	computeHourly, err := s.orgHourlyCost(ctx, orgID)
	if err != nil {
		return written, err
	}
	n, err := s.writeCostBucket(ctx, meterBucketID(orgID, hour), orgID, MeterMetric, computeHourly, hour)
	written += n
	if err != nil {
		return written, err
	}

	// Storage (provisioned database GB-hours at the live "storage" price).
	storageHourly, err := s.orgStorageHourlyCost(ctx, orgID)
	if err != nil {
		return written, err
	}
	n, err = s.writeCostBucket(ctx, dimBucketID(orgID, MeterMetricStorage, hour), orgID, MeterMetricStorage, storageHourly, hour)
	written += n
	if err != nil {
		return written, err
	}

	return written, nil
}

// writeCostBucket converts an hourly cost (in the price-list currency) to micro-cents
// and atomically records it under id/metric for the given hour. A non-positive cost
// is skipped (no zero rows). Returns 1 when a new record was inserted, else 0.
func (s *Service) writeCostBucket(ctx context.Context, id, orgID, metric string, hourly float64, hour time.Time) (int, error) {
	microCents := int64(math.Round(hourly * 100.0 * 1000.0)) // currency -> micro-cents
	if microCents <= 0 {
		return 0, nil
	}
	inserted, err := s.store.AddUsageIfAbsent(ctx, &domain.UsageRecord{
		ID:       id,
		OrgID:    orgID,
		Metric:   metric,
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

// RecordEgressGB records the cost of network egress for an org: gb gigabytes priced
// at the live admin "egress" price, stored as a micro-cents usage record under
// MeterMetricEgress at the current time. It is the honest entry point for egress
// metering — egress GB must come from a REAL measurement (e.g. a future metrics
// pipeline or a provider report), never fabricated (invariant #6). A non-positive gb
// or a missing/zero "egress" price is a no-op (the platform never invents traffic or
// a price). Egress that has been recorded flows into the period usage cost and the
// invoice's egress line item automatically.
func (s *Service) RecordEgressGB(ctx context.Context, orgID string, gb float64) error {
	if gb <= 0 {
		return nil
	}
	prices, err := s.priceMap(ctx)
	if err != nil {
		return err
	}
	rate := prices[priceKeyEgress]
	if rate <= 0 {
		return nil
	}
	microCents := int64(math.Round(gb * rate * 100.0 * 1000.0))
	if microCents <= 0 {
		return nil
	}
	return s.store.AddUsage(ctx, &domain.UsageRecord{
		ID:       s.idgen(),
		OrgID:    orgID,
		Metric:   MeterMetricEgress,
		Quantity: microCents,
		At:       s.now(),
	})
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
