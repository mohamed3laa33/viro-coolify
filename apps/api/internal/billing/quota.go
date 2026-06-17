package billing

// Limits is the per-plan resource ceiling enforced when provisioning apps and
// services. Values are upper bounds on a single workload's requested resources
// (and the org's total workload count for MaxApps). The concrete values come
// from the store-backed plan (see Service.PlanLimits).
type Limits struct {
	MaxCPU      float64 `json:"maxCpu"`      // max vCPU per workload
	MaxMemoryMB int     `json:"maxMemoryMb"` // max memory (MB) per workload
	MaxApps     int     `json:"maxApps"`     // max number of workloads per org
}
