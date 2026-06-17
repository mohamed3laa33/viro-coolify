// Package billing implements Viro's fly.io-style usage-based business model:
// a plan catalog, subscriptions, usage metering, and a pluggable payment
// provider (mock by default; Stripe when configured).
package billing

import "github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"

// Plans is the Viro catalog: a free Hobby tier plus usage-based paid tiers.
// StripePriceID is filled in per-environment when billing is live.
var Plans = []domain.Plan{
	{
		ID: "hobby", Name: "Hobby",
		Description:         "For side projects. Shared CPU, 1 app, community support.",
		PriceCents:          0, Currency: "usd",
		IncludedHours:       160, OveragePerHourCents: 0,
	},
	{
		ID: "launch", Name: "Launch",
		Description:         "For production apps. Dedicated CPU, autoscaling, custom domains.",
		PriceCents:          2900, Currency: "usd",
		IncludedHours:       720, OveragePerHourCents: 2,
	},
	{
		ID: "scale", Name: "Scale",
		Description:         "For scaling teams. Multi-region, higher limits, priority support.",
		PriceCents:          9900, Currency: "usd",
		IncludedHours:       2400, OveragePerHourCents: 1,
	},
}

// PlanByID returns a plan from the catalog.
func PlanByID(id string) (domain.Plan, bool) {
	for _, p := range Plans {
		if p.ID == id {
			return p, true
		}
	}
	return domain.Plan{}, false
}
