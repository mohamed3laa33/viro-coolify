// Package kube is Vortex's real Kubernetes deploy backend. It provisions tenant
// workloads as Helm releases of the team's `common-chart` into a per-org-project
// namespace, and reads status/logs back via client-go.
//
// There is NO demo / no-op success path here: KubeBackend talks to a real
// cluster (in-cluster config or a kubeconfig) and shells out to a real `helm`
// binary. For unit tests in OTHER packages, use FakeBackend (an in-memory test
// double that still implements the full Backend contract).
package kube

import (
	"context"
	"io"
)

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

	// Region is the (already-validated) placement region for this workload. It is
	// plumbed onto the rendered objects as a label (vortex.v60ai.com/region) and a
	// pod annotation, AND translated into real in-cluster scheduling
	// (nodeSelector/affinity + an optional regional-pool toleration; see
	// regionScheduling in values.go) so the pod lands on the node pool matching the
	// region within THIS cluster. Empty leaves the region label/annotation and all
	// scheduling constraints off entirely.
	//
	// Cross-CLUSTER multi-region (a separate cluster per physical region with
	// region-aware routing) is a documented from-scratch follow-up and is NOT done
	// here — this only steers placement within the single cluster.
	Region string

	// StorageGB is the persistent volume size (GiB) for a stateful (database)
	// workload. When >0 and Kind=="database", buildValues renders a
	// volumeClaimTemplate (data mount at the engine's data dir) with a RETAIN PVC
	// retention policy so Stop/scale/restart never wipe data. Ignored for
	// stateless app/service workloads.
	StorageGB int
	// StorageClass optionally overrides the PVC storageClassName for the data
	// volume. Empty leaves the cluster default / chart default in force.
	StorageClass string

	// ImagePullSecret, when set, names a kubernetes.io/dockerconfigjson Secret in
	// the workload's tenant namespace that is attached to the pod's
	// imagePullSecrets so a PRIVATE built image can be pulled. The platform sets
	// this for git-built apps (the per-tenant copy of the registry pull secret);
	// it is empty for public catalog images.
	ImagePullSecret string

	// EnvSecretName, when set, names a per-app Kubernetes Secret in the tenant
	// namespace whose keys are injected into the container via envFrom secretRef.
	// SECRET env values are delivered this way (never baked into the helm release
	// values), while non-secret config still flows through Env. The platform
	// creates/updates this Secret via EnsureAppSecret before Apply.
	EnvSecretName string

	// Scaling holds the admin/DB-driven KEDA autoscaling configuration (defaults
	// from platform settings, with per-app min/max overrides). The platform
	// populates it per Apply so buildValues renders the ScaledObject from live
	// settings rather than hardcoded constants. The zero value falls back to the
	// backend's built-in conservative defaults so a caller that does not set it
	// keeps working.
	Scaling Scaling
}

// Scaling is the admin/DB-driven KEDA autoscaling configuration for a workload.
// MinReplicas/MaxReplicas are already resolved (per-app override or platform
// default) by the platform layer; a stateful (database) workload is floored to a
// minimum of 1 by buildValues regardless of MinReplicas (never scale a database to
// zero). A MinReplicas of 0 on a stateless workload enables scale-to-zero.
type Scaling struct {
	MinReplicas     int
	MaxReplicas     int
	PollingInterval int  // seconds; <=0 falls back to a built-in default
	CooldownPeriod  int  // seconds; <=0 falls back to a built-in default
	CPUUtilization  int  // % target for the CPU trigger; <=0 falls back to a default
	HTTPTrigger     bool // add an HTTP-concurrency trigger (requires keda-http-add-on)
}

// Status is the observed runtime state of a workload's controller.
type Status struct {
	Phase         string // Running | Scaled to zero | Pending | Unknown
	Replicas      int
	ReadyReplicas int
}

// RolloutStatus is the detailed, deploy-progress view of a workload's
// Deployment/StatefulSet rollout, richer than Status. The UI renders a progress
// bar / health badge from it (e.g. "3/5 ready, 4 updated, Progressing"). All
// counts come straight from the controller's observed status — there is no
// fabricated progress (invariant #6).
type RolloutStatus struct {
	// Desired is the spec replica count the controller is converging toward.
	Desired int `json:"desired"`
	// Ready / Updated / Available mirror the controller's observed status:
	// Ready = pods passing readiness, Updated = pods on the newest pod-template
	// (the in-flight rollout's progress), Available = pods available for at least
	// minReadySeconds. Updated/Available are best-effort for a StatefulSet (it
	// exposes UpdatedReplicas but not AvailableReplicas — Available then mirrors
	// Ready).
	Ready     int `json:"ready"`
	Updated   int `json:"updated"`
	Available int `json:"available"`

	// ObservedGeneration / Generation let the caller detect a stale controller
	// status: when ObservedGeneration < Generation the controller has not yet
	// acted on the latest spec (the rollout is still being picked up).
	ObservedGeneration int64 `json:"observedGeneration"`
	Generation         int64 `json:"generation"`

	// Health is the derived rollout health, one of:
	//   "complete"    — Desired==Ready==Updated and the controller is up to date
	//   "progressing" — a rollout is in flight (or scaling up) and on track
	//   "degraded"    — the controller reports a failure (ReplicaFailure / a
	//                   Progressing=False condition, e.g. ProgressDeadlineExceeded)
	//   "scaled-zero" — Desired==0 (stopped / scaled to zero)
	//   "unknown"     — no controller found / status not yet observed
	Health string `json:"health"`
	// Phase is the same coarse phase string Status reports, kept for callers that
	// only need the one-word state (Running | Pending | Scaled to zero | ...).
	Phase string `json:"phase"`
	// Reason / Message surface the controller condition that drove a non-complete
	// Health (e.g. reason "ProgressDeadlineExceeded"), so the UI can show WHY a
	// deploy is stuck rather than just spinning. Empty when complete/healthy.
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

// BackupSpec describes a one-shot (or scheduled) logical database backup. The
// platform layer populates it from the stored domain.Database (engine, namespace,
// release, connection credentials) plus the admin/DB-driven backup settings
// (image, storage size/class, schedule); none of these are hardcoded here
// (invariant #1). The dump runs as a Kubernetes Job (or CronJob when Schedule is
// set) writing an engine-appropriate dump file to a per-database backup PVC.
type BackupSpec struct {
	Namespace string // tenant namespace the database lives in
	Release   string // the database's Helm release / Service name (in-cluster host)
	Engine    string // postgresql | mysql | mariadb | mongodb | redis (template key)

	// In-cluster connection details for the dump tool. Host defaults to Release
	// (the ClusterIP Service name) when empty; Port defaults to the engine default.
	Host     string
	Port     int
	Database string // logical database name to dump
	Username string
	Password string

	// Image is the dump/restore client image (admin/DB-driven, e.g. a pinned
	// "postgres:16" whose pg_dump/psql match the server). Empty falls back to an
	// engine-default client image so a backup still runs in dev.
	Image string

	// StorageGB / StorageClass size the per-database backup PVC. StorageGB<=0
	// clamps to a safe minimum; StorageClass empty uses the cluster default.
	StorageGB    int
	StorageClass string

	// Schedule, when non-empty, makes EnsureBackupSchedule render a CronJob with
	// this cron expression (e.g. "0 3 * * *" for a daily 03:00 dump) instead of a
	// one-shot Job. BackupDatabase always runs a one-shot Job (ignores Schedule).
	Schedule string
}

// RestoreSpec describes restoring a named backup file into a database. It mirrors
// BackupSpec's connection details and names the dump file (as returned by
// ListDatabaseBackups) to restore from the backup PVC.
type RestoreSpec struct {
	Namespace string
	Release   string
	Engine    string

	Host     string
	Port     int
	Database string
	Username string
	Password string

	Image string

	// BackupName is the dump file (DatabaseBackup.Name) on the backup PVC to
	// restore from. Required.
	BackupName string
}

// DatabaseBackup is one observed backup artifact / run. For an on-demand backup
// it is the Job created by BackupDatabase; ListDatabaseBackups returns every
// backup Job (newest first) with its observed phase so the UI can show backup
// history and offer a restore. There is no fabricated "succeeded" — Phase
// reflects the Job's real condition (invariant #6).
type DatabaseBackup struct {
	// Name is the backup Job name (and the basis for the dump file name). It is
	// what RestoreSpec.BackupName references.
	Name string `json:"name"`
	// Phase is the backup run state derived from the Job status:
	//   "Running" | "Succeeded" | "Failed" | "Pending" | "Unknown".
	Phase string `json:"phase"`
	// CreatedAt / CompletedAt are the Job's creation and completion timestamps
	// (RFC3339); CompletedAt is empty while the backup is still running.
	CreatedAt   string `json:"createdAt"`
	CompletedAt string `json:"completedAt,omitempty"`
	// Engine echoes the database engine the backup was taken from.
	Engine string `json:"engine"`
	// Scheduled is true when this run was created by the backup CronJob (vs. an
	// on-demand BackupDatabase call).
	Scheduled bool `json:"scheduled"`
}

// PodMetric is a single workload pod's live resource usage as read from the
// Kubernetes metrics-server (metrics.k8s.io/v1beta1 PodMetrics). CPU is in
// millicores, memory in bytes — summed across the pod's containers.
type PodMetric struct {
	Pod           string `json:"pod"`
	CPUMillicores int64  `json:"cpuMillicores"`
	MemoryBytes   int64  `json:"memoryBytes"`
}

// WorkloadMetrics is a live, point-in-time snapshot of a workload's pod resource
// usage from the metrics-server. Available is false (with an Unavailable reason)
// when the metrics-server is not installed/reachable — the caller then surfaces an
// HONEST "metrics unavailable" rather than any fabricated number. When Available
// is true, Pods carries the per-pod usage and CPUMillicores/MemoryBytes the
// aggregate across all pods.
type WorkloadMetrics struct {
	Available     bool        `json:"available"`
	Unavailable   string      `json:"unavailable,omitempty"` // reason when Available is false
	Pods          []PodMetric `json:"pods"`
	CPUMillicores int64       `json:"cpuMillicores"` // aggregate
	MemoryBytes   int64       `json:"memoryBytes"`   // aggregate
}

// LogStreamOptions controls a live log stream.
type LogStreamOptions struct {
	// Follow keeps the stream open and forwards new lines as they are written.
	Follow bool
	// TailLines, when >0, seeds the stream with the last N lines before following.
	TailLines int
	// Previous streams the logs of the previous (crashed) container instance.
	Previous bool
	// AllPods, when true, multiplexes every workload pod's logs (each line is
	// prefixed with its pod name); otherwise only the newest pod is streamed.
	AllPods bool
}

// Backend is the deploy surface the platform layer talks to. Implementations:
// KubeBackend (real) and FakeBackend (test double).
type Backend interface {
	// EnsureTenant creates (idempotently) the org-project namespace plus a
	// ResourceQuota and LimitRange derived from the plan quota, returning the
	// namespace name.
	EnsureTenant(ctx context.Context, orgSlug, projSlug string, q Quota) (namespace string, err error)

	// EnsureImagePullSecret upserts a kubernetes.io/dockerconfigjson Secret named
	// "name" in tenant namespace "ns" by copying the dockerconfigjson from a
	// configured control-plane source secret, so a private built image can be
	// pulled. When no registry pull secret source is configured (local/dev) it is
	// a no-op (returns nil) so non-registry flows keep working.
	EnsureImagePullSecret(ctx context.Context, ns, name string) error

	// EnsureAppSecret creates/updates an Opaque Kubernetes Secret named "name" in
	// tenant namespace "ns" holding the workload's SECRET env (key -> plaintext
	// value, supplied already-decrypted by the platform). The chart references it
	// via envFrom secretRef, so secret values are never baked into the helm
	// release values. An empty data map deletes the Secret (no stale secrets
	// linger after the last secret is removed).
	EnsureAppSecret(ctx context.Context, ns, name string, data map[string]string) error

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
	// LogStream streams the release's pod logs to w. With opts.Follow it blocks,
	// forwarding new lines until ctx is cancelled (client disconnect) or the
	// stream ends; without Follow it writes a one-shot snapshot and returns. The
	// caller is responsible for flushing w per line (e.g. an SSE writer).
	LogStream(ctx context.Context, namespace, release string, opts LogStreamOptions, w io.Writer) error
	// Status reports replica counts for the release's Deployment/StatefulSet.
	Status(ctx context.Context, namespace, release string) (Status, error)
	// AppRolloutStatus reports the detailed deploy-progress view of the release's
	// Deployment/StatefulSet rollout (desired/ready/updated/available replicas,
	// observed vs. spec generation, and a derived health/condition) so the UI can
	// render live deploy progress. A missing controller returns
	// RolloutStatus{Health:"unknown"} with an error.
	AppRolloutStatus(ctx context.Context, namespace, release string) (RolloutStatus, error)
	// Metrics reads the live per-pod CPU/memory usage for the release from the
	// metrics-server. When the metrics-server is unavailable it returns
	// WorkloadMetrics{Available:false} (never fabricated numbers), not an error.
	Metrics(ctx context.Context, namespace, release string) (WorkloadMetrics, error)

	// BackupDatabase runs a one-shot, engine-appropriate logical backup
	// (pg_dump / mysqldump / mongodump / redis SAVE) of the database as a
	// Kubernetes Job, writing the dump to a per-database backup PVC (ensured
	// idempotently). It returns the created DatabaseBackup descriptor (whose Name
	// is the restore handle). It does NOT wait for the dump to finish — the caller
	// polls ListDatabaseBackups for the run's phase. An unsupported engine returns
	// an error.
	BackupDatabase(ctx context.Context, spec BackupSpec) (DatabaseBackup, error)
	// EnsureBackupSchedule installs/updates a CronJob that runs the same dump on
	// spec.Schedule (e.g. a daily "0 3 * * *"). It is the data-durability default:
	// the platform ensures it on database create so backups happen without manual
	// action. An empty Schedule removes any existing CronJob (backups disabled).
	EnsureBackupSchedule(ctx context.Context, spec BackupSpec) error
	// ListDatabaseBackups returns the database's backup runs (on-demand Jobs and
	// the latest CronJob-spawned Jobs), newest first, each with its observed phase
	// so the UI can show history and offer a restore. A namespace/release with no
	// backups returns an empty slice (not an error).
	ListDatabaseBackups(ctx context.Context, namespace, release string) ([]DatabaseBackup, error)
	// RestoreDatabase runs a Job that restores spec.BackupName (a dump file on the
	// backup PVC) into the live database via the engine's restore client
	// (psql / mysql / mongorestore). DESTRUCTIVE: it overwrites current data, so
	// the platform/handler layer must gate it behind an explicit confirmation. It
	// returns when the restore Job is SUBMITTED (the caller polls for completion).
	RestoreDatabase(ctx context.Context, spec RestoreSpec) error

	// EnsureDomainCertificate creates (idempotently) a cert-manager Certificate for
	// a VERIFIED custom hostname, signed by the platform ClusterIssuer, into a TLS
	// Secret in the shared Gateway namespace. Called when a domain becomes verified.
	EnsureDomainCertificate(ctx context.Context, host string) error
	// RemoveDomainCertificate deletes the per-domain Certificate (its Secret is GC'd
	// by cert-manager). Idempotent; called on domain delete.
	RemoveDomainCertificate(ctx context.Context, host string) error
	// EnsureGatewayListener adds (idempotently) a dedicated HTTPS listener for the
	// custom host to a Gateway in the SHARD POOL, terminating TLS with certSecret.
	// It merges into spec.listeners without clobbering other tenants' listeners and
	// AUTO-SHARDS across additional Gateways once the primary fills up (so the
	// platform scales past one Gateway's 64-listener ceiling without manual
	// intervention). Called when a domain becomes verified.
	EnsureGatewayListener(ctx context.Context, host, certSecret string) error
	// RemoveGatewayListener removes the per-domain listener from whichever shard in
	// the pool holds it, preserving all others (and GC'ing an emptied overflow
	// shard). Idempotent; called on domain delete.
	RemoveGatewayListener(ctx context.Context, host string) error

	// EnsureOrgWildcard provisions (idempotently) a per-org wildcard so the
	// platform-generated tenant host <app>.<project>.<org>.<baseDomain> terminates
	// TLS and routes through the SHARED Gateway. The base bootstrap wildcard only
	// covers ONE label (*.<baseDomain>), so without this an org's tenant apps would
	// have no matching certificate or listener and HTTPS would fail on first use.
	//
	// It issues a single cert-manager Certificate covering the org subtree
	// (*.<org>.<baseDomain> for project-level hosts, plus *.<project>.<org>.<baseDomain>
	// for every supplied project so the 3-label app host is genuinely covered — a
	// DNS/TLS wildcard matches exactly one label, so the org wildcard alone stops at
	// the project label) and adds a matching HTTPS listener to the shared Gateway for
	// each wildcard, merging without clobbering other tenants' listeners.
	//
	// projectSlugs are the org's known projects at call time (the default project
	// always exists at org creation); passing more later re-issues the cert with the
	// added SANs and adds the new listeners. With no ClusterIssuer/dynamic client
	// configured (local/dev) it no-ops so non-cluster flows keep working.
	EnsureOrgWildcard(ctx context.Context, orgSlug string, projectSlugs []string) error
}
