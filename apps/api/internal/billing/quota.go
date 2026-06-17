package billing

// Limits is the per-plan resource ceiling enforced when provisioning apps and
// services. Values are upper bounds on a single workload's requested resources
// (and the org's total workload count for MaxApps).
type Limits struct {
	MaxCPU      float64 `json:"maxCpu"`      // max vCPU per workload
	MaxMemoryMB int     `json:"maxMemoryMb"` // max memory (MB) per workload
	MaxApps     int     `json:"maxApps"`     // max number of workloads per org
}

// planLimits maps a plan id to its resource limits.
var planLimits = map[string]Limits{
	"hobby":  {MaxCPU: 0.5, MaxMemoryMB: 512, MaxApps: 3},
	"launch": {MaxCPU: 1, MaxMemoryMB: 1024, MaxApps: 20},
	"scale":  {MaxCPU: 2, MaxMemoryMB: 4096, MaxApps: 100},
}

// PlanLimits returns the resource limits for the given plan id, defaulting to the
// hobby tier for unknown or empty plans.
func PlanLimits(planID string) Limits {
	if l, ok := planLimits[planID]; ok {
		return l
	}
	return planLimits["hobby"]
}
