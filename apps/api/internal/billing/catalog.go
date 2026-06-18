// Package billing implements Viro's fly.io-style usage-based business model:
// a plan catalog, subscriptions, usage metering, and a pluggable payment
// provider (mock by default; Stripe when configured).
//
// The plan catalog and per-plan resource limits are stored in the control-plane
// store and managed via the super-admin API; this package reads them from the
// store rather than holding any hardcoded source of truth.
package billing

import (
	"context"
	"errors"
	"sort"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/store"
)

// Catalog returns the active plans, sorted by SortOrder, from the store. A store
// error is PROPAGATED (not swallowed as an empty catalog) so a transient DB
// failure surfaces as a 5xx instead of a 200 with an empty/null plan list.
func (s *Service) Catalog(ctx context.Context) ([]domain.Plan, error) {
	plans, err := s.store.ListPlans(ctx)
	if err != nil {
		return nil, err
	}
	active := make([]domain.Plan, 0, len(plans))
	for _, p := range plans {
		if p.Active {
			active = append(active, p)
		}
	}
	sort.Slice(active, func(i, j int) bool { return active[i].SortOrder < active[j].SortOrder })
	return active, nil
}

// PlanByID returns a plan from the store catalog by id. found=false with a nil
// error means "no such plan"; a non-nil error is a real store failure and is
// propagated rather than masked as not-found.
func (s *Service) PlanByID(ctx context.Context, id string) (plan domain.Plan, found bool, err error) {
	p, err := s.store.GetPlan(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return domain.Plan{}, false, nil
	}
	if err != nil {
		return domain.Plan{}, false, err
	}
	return *p, true, nil
}

// defaultPlan returns the store's default plan (IsDefault), falling back to the
// first plan it finds. The bool is false when there are no plans at all.
func (s *Service) defaultPlan(ctx context.Context) (domain.Plan, bool) {
	plans, err := s.store.ListPlans(ctx)
	if err != nil || len(plans) == 0 {
		return domain.Plan{}, false
	}
	for _, p := range plans {
		if p.IsDefault {
			return p, true
		}
	}
	return plans[0], true
}

// PlanLimits returns the resource limits for the given plan id, falling back to
// the default plan for unknown or empty plans.
func (s *Service) PlanLimits(ctx context.Context, planID string) Limits {
	if p, err := s.store.GetPlan(ctx, planID); err == nil {
		return limitsOf(*p)
	}
	if p, ok := s.defaultPlan(ctx); ok {
		return limitsOf(p)
	}
	return Limits{}
}

func limitsOf(p domain.Plan) Limits {
	return Limits{MaxCPU: p.MaxCPU, MaxMemoryMB: p.MaxMemoryMB, MaxApps: p.MaxApps}
}
