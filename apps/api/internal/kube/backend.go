// Package kube is Vortex's real Kubernetes deploy backend. It provisions tenant
// workloads as Helm releases of the team's `common-chart` into a per-org-project
// namespace, and reads status/logs back via client-go.
//
// There is NO demo / no-op success path here: KubeBackend talks to a real
// cluster (in-cluster config or a kubeconfig) and shells out to a real `helm`
// binary. For unit tests in OTHER packages, use FakeBackend (an in-memory test
// double that still implements the full Backend contract).
package kube

import "context"

// Quota is the per-tenant resource ceiling, derived from the org's billing plan.
// It is translated into a Kubernetes ResourceQuota + LimitRange on the namespace.
type Quota struct {
	MaxCPU      float64 // total schedulable vCPU across the namespace (limits ceiling)
	MaxMemoryMB int     // total memory (MB) across the namespace
	MaxApps     int     // max workloads (pods/deployments) in the namespace

	// Per-container LimitRange defaults (admin/DB-driven via platform settings).
	// The default LIMIT is the minimal workload size; the default REQUEST applies
	// the overcommit factor to it. Zero values fall back to minimal built-ins.
	DefaultCPU             float64
	DefaultMemoryMB        int
	CPUOvercommitFactor    float64
	MemoryOvercommitFactor float64
}

// Workload is one tenant deployable (an app, a one-click service, or a database).
// CPU/MemoryMB are the REQUESTED (advertised/billed) size; the overcommit math in
// Apply derives the much smaller scheduler requests from these.
type Workload struct {
	OrgSlug     string
	ProjectSlug string
	Name        string
	Kind        string // app | service | database

	Image    string // resolved image ref from the catalog template / build
	Port     int    // container/service port; 0 falls back to the engine default
	CPU      float64
	MemoryMB int
	Env      map[string]string

	// Overcommit factors, sourced live from admin/DB platform settings per Apply.
	// Zero means "use the backend's configured default" (so callers that don't set
	// them keep working).
	CPUOvercommitFactor    float64
	MemoryOvercommitFactor float64

	ServiceTemplateKey string   // e.g. wordpress, redis, postgresql
	Domains            []string // extra custom hostnames in addition to the generated host
}

// Status is the observed runtime state of a workload's controller.
type Status struct {
	Phase         string // Running | Scaled to zero | Pending | Unknown
	Replicas      int
	ReadyReplicas int
}

// Backend is the deploy surface the platform layer talks to. Implementations:
// KubeBackend (real) and FakeBackend (test double).
type Backend interface {
	// EnsureTenant creates (idempotently) the org-project namespace plus a
	// ResourceQuota and LimitRange derived from the plan quota, returning the
	// namespace name.
	EnsureTenant(ctx context.Context, orgSlug, projSlug string, q Quota) (namespace string, err error)

	// Apply renders chart values for the workload and runs `helm upgrade
	// --install`, returning the Helm release name and the generated public host.
	Apply(ctx context.Context, w Workload) (release string, host string, err error)

	// Start scales a stopped workload back up.
	Start(ctx context.Context, namespace, release string) error
	// Stop scales the workload to zero replicas (release is retained).
	Stop(ctx context.Context, namespace, release string) error
	// Restart triggers a rollout restart of the workload's pods.
	Restart(ctx context.Context, namespace, release string) error
	// Delete uninstalls the Helm release.
	Delete(ctx context.Context, namespace, release string) error

	// Logs returns the most recent pod logs for the release (tailLines lines).
	Logs(ctx context.Context, namespace, release string, tailLines int) (string, error)
	// Status reports replica counts for the release's Deployment/StatefulSet.
	Status(ctx context.Context, namespace, release string) (Status, error)
}
