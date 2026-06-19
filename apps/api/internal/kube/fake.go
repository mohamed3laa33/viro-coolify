package kube

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// FakeBackend is an in-memory Backend test double for unit tests in OTHER
// packages (e.g. platform). It records every workload and returns deterministic
// hosts/status — replacing the old "demo mode" with a real, inspectable double.
//
// It is safe for concurrent use.
type FakeBackend struct {
	// BaseDomain mirrors KubeBackend's host derivation so callers see realistic
	// hostnames. Defaults to "vortex.v60ai.com" when empty.
	BaseDomain string

	mu sync.Mutex

	// Tenants maps namespace -> the quota it was ensured with.
	Tenants map[string]Quota
	// Applied records every Apply call, keyed by "<namespace>/<release>".
	Applied map[string]Workload
	// Hosts records the generated host per "<namespace>/<release>".
	Hosts map[string]string
	// Replicas records the desired replica count per "<namespace>/<release>".
	Replicas map[string]int
	// PullSecrets records every EnsureImagePullSecret call as "<namespace>/<name>".
	PullSecrets map[string]bool
	// AppSecrets records the latest EnsureAppSecret data keyed by "<namespace>/<name>".
	// A nil/empty data map records the deletion (entry removed).
	AppSecrets map[string]map[string]string
	// LogLines is the canned log output returned by Logs (and, split per line, by
	// LogStream).
	LogLines string

	// DomainCerts records every host an EnsureDomainCertificate was issued for and
	// removes it on RemoveDomainCertificate, so domain-TLS tests can assert that a
	// VERIFIED custom domain got a cert (and a deleted one got it removed).
	DomainCerts map[string]bool
	// GatewayListeners records each host attached to the shared Gateway via
	// EnsureGatewayListener (cleared by RemoveGatewayListener), mapping host ->
	// cert Secret name so tests can assert the listener references the right cert.
	GatewayListeners map[string]string

	// OrgWildcards records each org slug an EnsureOrgWildcard was provisioned for,
	// mapping org slug -> the project slugs included, so org-TLS tests can assert a
	// new org got its per-org wildcard cert + listeners (and which projects were
	// covered).
	OrgWildcards map[string][]string

	// HTTPWakes records, per "<namespace>/<release>", the FQDN host(s) an HTTP
	// scale-to-zero app was wired for WAKE via a keda-add-ons-http HTTPScaledObject
	// (set by Apply when the workload opts into HTTP wake, cleared when it does not),
	// so platform/handler tests can assert that an HTTP/web app with scale-to-zero
	// got the interceptor-driven wake wiring (and a worker / non-zero-floor app did
	// NOT). It is an HONEST record — an entry only appears because Apply saw a
	// wake-eligible workload, never a fabricated success.
	HTTPWakes map[string][]string

	// GatewayShardOf records which Gateway SHARD each attached custom-domain host
	// landed on (host -> Gateway name), mirroring the real backend's auto-sharding
	// (gateway_shard.go). Shard 0 is the primary Gateway (FakeGatewayName); once a
	// shard reaches FakeShardBudget custom-domain listeners, the next host overflows
	// to a freshly-named "<primary>-shard-<n>" Gateway. Tests assert that the pool
	// scales past one Gateway's listener ceiling without error.
	GatewayShardOf map[string]string
	// FakeGatewayName is the primary (shard 0) Gateway name used when simulating
	// sharding. Defaults to "vortex" when empty.
	FakeGatewayName string
	// FakeShardBudget is the per-shard custom-domain listener capacity the fake
	// simulates before overflowing to a new shard. Defaults to maxGatewayListeners
	// (minus the primary's reserved base listeners on shard 0) when <= 0.
	FakeShardBudget int

	// PhaseOverride, when set for a "<namespace>/<release>" key, forces Status to
	// report that Phase verbatim (e.g. "Failed") instead of deriving it from the
	// replica count. It lets tests drive the reconciler down the failed/pending
	// paths the replica-count derivation can't otherwise produce.
	PhaseOverride map[string]string

	// RolloutOverride, when set for a "<namespace>/<release>" key, forces
	// AppRolloutStatus to return that RolloutStatus verbatim, letting tests drive
	// the UI/handler down the progressing/degraded paths the replica-count
	// derivation can't otherwise produce.
	RolloutOverride map[string]RolloutStatus

	// Backups records every database backup created via BackupDatabase, keyed by
	// "<namespace>/<release>" (newest appended last). ListDatabaseBackups returns
	// them newest first. It is an HONEST in-memory record, not a fake-success
	// path: a backup only appears here because BackupDatabase was actually called.
	Backups map[string][]DatabaseBackup
	// BackupSchedules records the latest EnsureBackupSchedule schedule per
	// "<namespace>/<release>" (empty schedule removes the entry).
	BackupSchedules map[string]string
	// Restores records every RestoreDatabase call as a "<namespace>/<release>" ->
	// list of restored backup names, so tests can assert a restore was requested.
	Restores map[string][]string

	// MetricsAvailable controls whether Metrics reports live data. When false the
	// fake mimics a cluster without a metrics-server (honest "unavailable").
	MetricsAvailable bool
	// CPUMillicores / MemoryBytes are the deterministic per-pod usage the fake
	// reports when MetricsAvailable is true.
	CPUMillicores int64
	MemoryBytes   int64
}

var _ Backend = (*FakeBackend)(nil)

// NewFakeBackend returns an initialized FakeBackend.
func NewFakeBackend() *FakeBackend {
	return &FakeBackend{
		BaseDomain:       "vortex.v60ai.com",
		Tenants:          map[string]Quota{},
		Applied:          map[string]Workload{},
		Hosts:            map[string]string{},
		Replicas:         map[string]int{},
		PullSecrets:      map[string]bool{},
		AppSecrets:       map[string]map[string]string{},
		DomainCerts:      map[string]bool{},
		GatewayListeners: map[string]string{},
		OrgWildcards:     map[string][]string{},
		HTTPWakes:        map[string][]string{},
		GatewayShardOf:   map[string]string{},
		FakeGatewayName:  "vortex",
		PhaseOverride:    map[string]string{},
		RolloutOverride:  map[string]RolloutStatus{},
		Backups:          map[string][]DatabaseBackup{},
		BackupSchedules:  map[string]string{},
		Restores:         map[string][]string{},
		LogLines:         "fake log line\n",
		// Deterministic test values: a deployed workload reports a fixed live usage
		// so platform/handler tests can assert REAL (non-synthetic) numbers.
		MetricsAvailable: true,
		CPUMillicores:    125,
		MemoryBytes:      64 * 1024 * 1024,
	}
}

func (f *FakeBackend) baseDomain() string {
	if f.BaseDomain == "" {
		return "vortex.v60ai.com"
	}
	return f.BaseDomain
}

func key(ns, rel string) string { return ns + "/" + rel }

func (f *FakeBackend) EnsureTenant(_ context.Context, orgSlug, projSlug string, q Quota) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ns := namespaceName(orgSlug, projSlug)
	f.Tenants[ns] = q
	return ns, nil
}

func (f *FakeBackend) EnsureImagePullSecret(_ context.Context, ns, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.PullSecrets[ns+"/"+name] = true
	return nil
}

func (f *FakeBackend) EnsureAppSecret(_ context.Context, ns, name string, data map[string]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := ns + "/" + name
	if len(data) == 0 {
		delete(f.AppSecrets, k)
		return nil
	}
	cp := make(map[string]string, len(data))
	for dk, dv := range data {
		cp[dk] = dv
	}
	f.AppSecrets[k] = cp
	return nil
}

func (f *FakeBackend) Apply(_ context.Context, w Workload) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ns := namespaceName(w.OrgSlug, w.ProjectSlug)
	rel := releaseName(w.Name)
	h := host(w.Name, w.ProjectSlug, w.OrgSlug, f.baseDomain())
	k := key(ns, rel)
	f.Applied[k] = w
	f.Hosts[k] = h
	f.Replicas[k] = 1

	// Mirror the real backend's HTTP scale-to-zero WAKE decision: an HTTP/web app
	// that opts in and scales to zero gets a keda-add-ons-http HTTPScaledObject keyed
	// on its FQDN(s); every other workload has any stale wake wiring removed. Record
	// the host set (or clear it) so tests can assert the wiring honestly.
	stateful := strings.EqualFold(w.Kind, "database")
	if wantsHTTPWake(w, stateful) {
		f.HTTPWakes[k] = append([]string{h}, sanitizeDomains(w.Domains)...)
	} else {
		delete(f.HTTPWakes, k)
	}
	return rel, h, nil
}

func (f *FakeBackend) Start(_ context.Context, namespace, release string) error {
	return f.setReplicas(namespace, release, 1)
}

func (f *FakeBackend) Stop(_ context.Context, namespace, release string) error {
	return f.setReplicas(namespace, release, 0)
}

func (f *FakeBackend) Restart(_ context.Context, namespace, release string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.Applied[key(namespace, release)]; !ok {
		return fmt.Errorf("kube(fake): no release %q in %q", release, namespace)
	}
	return nil
}

func (f *FakeBackend) Delete(_ context.Context, namespace, release string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := key(namespace, release)
	delete(f.Applied, k)
	delete(f.Hosts, k)
	delete(f.Replicas, k)
	delete(f.HTTPWakes, k)
	return nil
}

func (f *FakeBackend) setReplicas(namespace, release string, n int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := key(namespace, release)
	if _, ok := f.Applied[k]; !ok {
		return fmt.Errorf("kube(fake): no release %q in %q", release, namespace)
	}
	f.Replicas[k] = n
	return nil
}

func (f *FakeBackend) Logs(_ context.Context, namespace, release string, _ int) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.Applied[key(namespace, release)]; !ok {
		return "", fmt.Errorf("kube(fake): no release %q in %q", release, namespace)
	}
	return f.LogLines, nil
}

// LogStream writes the canned LogLines to w (one write per line, prefixed for
// the all-pods case so multi-pod behavior is observable). For a non-follow stream
// it returns after the snapshot; for a follow stream it returns when the snapshot
// is written or ctx is cancelled (whichever first) — deterministic for tests.
func (f *FakeBackend) LogStream(ctx context.Context, namespace, release string, opts LogStreamOptions, w io.Writer) error {
	f.mu.Lock()
	_, ok := f.Applied[key(namespace, release)]
	lines := f.LogLines
	f.mu.Unlock()
	if !ok {
		return fmt.Errorf("kube(fake): no release %q in %q", release, namespace)
	}
	prefix := ""
	if opts.AllPods {
		prefix = "[" + release + "-0] "
	}
	for _, line := range strings.Split(strings.TrimRight(lines, "\n"), "\n") {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if _, err := io.WriteString(w, prefix+line+"\n"); err != nil {
			return err
		}
	}
	return nil
}

// Metrics returns the fake's deterministic live usage for a deployed release, or
// an honest "unavailable" snapshot when MetricsAvailable is false (no fabrication).
func (f *FakeBackend) Metrics(_ context.Context, namespace, release string) (WorkloadMetrics, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.Applied[key(namespace, release)]; !ok {
		return WorkloadMetrics{}, fmt.Errorf("kube(fake): no release %q in %q", release, namespace)
	}
	if !f.MetricsAvailable {
		return WorkloadMetrics{Available: false, Unavailable: "metrics-server unavailable"}, nil
	}
	return WorkloadMetrics{
		Available:     true,
		Pods:          []PodMetric{{Pod: release + "-0", CPUMillicores: f.CPUMillicores, MemoryBytes: f.MemoryBytes}},
		CPUMillicores: f.CPUMillicores,
		MemoryBytes:   f.MemoryBytes,
	}, nil
}

// EnsureDomainCertificate records that a per-domain TLS cert was issued for host.
func (f *FakeBackend) EnsureDomainCertificate(_ context.Context, host string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.DomainCerts[strings.ToLower(strings.TrimSpace(host))] = true
	return nil
}

// RemoveDomainCertificate clears the recorded per-domain TLS cert for host.
func (f *FakeBackend) RemoveDomainCertificate(_ context.Context, host string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.DomainCerts, strings.ToLower(strings.TrimSpace(host)))
	return nil
}

// EnsureGatewayListener records that host was attached to a Gateway in the shard
// pool with the given cert Secret (defaulting to the derived per-domain Secret
// name), simulating the real backend's auto-sharding: it places the host on the
// first shard with capacity, overflowing to a new "<primary>-shard-<n>" Gateway
// once each is full, so the fake NEVER errors at a 64-listener ceiling. Idempotent
// per host (re-attaching keeps the existing placement).
func (f *FakeBackend) EnsureGatewayListener(_ context.Context, host, certSecret string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	host = strings.ToLower(strings.TrimSpace(host))
	if certSecret == "" {
		certSecret = DomainCertSecret(host)
	}
	if _, ok := f.GatewayListeners[host]; !ok {
		// New host: allocate it to a shard.
		f.GatewayShardOf[host] = f.allocateFakeShard()
	}
	f.GatewayListeners[host] = certSecret
	return nil
}

// fakePrimaryGateway returns the configured primary (shard 0) Gateway name.
func (f *FakeBackend) fakePrimaryGateway() string {
	if f.FakeGatewayName == "" {
		return "vortex"
	}
	return f.FakeGatewayName
}

// fakeShardBudget is the simulated per-shard custom-domain listener capacity.
func (f *FakeBackend) fakeShardBudget() int {
	if f.FakeShardBudget > 0 {
		return f.FakeShardBudget
	}
	return maxGatewayListeners
}

// allocateFakeShard returns the Gateway name the next NEW custom-domain host
// should land on, mirroring allocateListenerShard: the lowest-index shard with
// remaining capacity, else a brand-new shard. The primary (shard 0) reserves
// baseListenerReserve slots for its wildcard/http listeners. The fake counts only
// custom-domain placements (it does not model the primary's wildcard listener
// object), so its budget is approximate vs the real backend — it models the
// BEHAVIOR (overflow without error, monotonic shard growth), not byte-exact
// counts. Caller holds f.mu.
func (f *FakeBackend) allocateFakeShard() string {
	primary := f.fakePrimaryGateway()
	budget := f.fakeShardBudget()

	// Count current custom-domain listeners per shard.
	counts := map[string]int{}
	for _, gw := range f.GatewayShardOf {
		counts[gw]++
	}
	// Highest existing shard index (to know where the next new shard goes).
	maxIdx := 0
	for gw := range counts {
		if idx, ok := gatewayShardIndex(primary, gw); ok && idx > maxIdx {
			maxIdx = idx
		}
	}
	// Walk shards 0..maxIdx looking for free capacity.
	for idx := 0; idx <= maxIdx; idx++ {
		name := gatewayShardName(primary, idx)
		shardCap := budget
		if idx == 0 {
			shardCap -= baseListenerReserve
		}
		if counts[name] < shardCap {
			return name
		}
	}
	// All full (or pool empty): the next shard. When the pool is empty this is the
	// primary (idx 0); otherwise it is maxIdx+1.
	if len(counts) == 0 {
		return primary
	}
	return gatewayShardName(primary, maxIdx+1)
}

// RemoveGatewayListener clears the recorded shard-pool listener for host (and its
// shard placement). Idempotent.
func (f *FakeBackend) RemoveGatewayListener(_ context.Context, host string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	host = strings.ToLower(strings.TrimSpace(host))
	delete(f.GatewayListeners, host)
	delete(f.GatewayShardOf, host)
	return nil
}

// EnsureOrgWildcard records that a per-org wildcard cert + listeners were
// provisioned for orgSlug (with the supplied project slugs). It is idempotent and
// MERGES projects across calls so re-provisioning with more projects accumulates
// coverage, mirroring the real backend re-issuing the cert with added SANs.
func (f *FakeBackend) EnsureOrgWildcard(_ context.Context, orgSlug string, projectSlugs []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	orgSlug = strings.ToLower(strings.TrimSpace(orgSlug))
	if orgSlug == "" {
		return nil
	}
	seen := map[string]bool{}
	merged := make([]string, 0, len(f.OrgWildcards[orgSlug])+len(projectSlugs))
	for _, p := range append(append([]string{}, f.OrgWildcards[orgSlug]...), projectSlugs...) {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		merged = append(merged, p)
	}
	f.OrgWildcards[orgSlug] = merged
	return nil
}

func (f *FakeBackend) Status(_ context.Context, namespace, release string) (Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := key(namespace, release)
	if _, ok := f.Applied[k]; !ok {
		return Status{Phase: "Unknown"}, fmt.Errorf("kube(fake): no release %q in %q", release, namespace)
	}
	n := f.Replicas[k]
	if ph, ok := f.PhaseOverride[k]; ok {
		return Status{Phase: ph, Replicas: n, ReadyReplicas: n}, nil
	}
	phase := "Running"
	if n == 0 {
		phase = "Scaled to zero"
	}
	return Status{Phase: phase, Replicas: n, ReadyReplicas: n}, nil
}

// AppRolloutStatus returns a deterministic rollout view for a deployed release
// derived from its recorded replica count (every desired replica is ready/updated
// — the fake double has no real rollout in flight), or a test-supplied override.
func (f *FakeBackend) AppRolloutStatus(_ context.Context, namespace, release string) (RolloutStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := key(namespace, release)
	if _, ok := f.Applied[k]; !ok {
		return RolloutStatus{Health: healthUnknown, Phase: "Unknown"},
			fmt.Errorf("kube(fake): no release %q in %q", release, namespace)
	}
	if rs, ok := f.RolloutOverride[k]; ok {
		return rs, nil
	}
	n := f.Replicas[k]
	rs := RolloutStatus{
		Desired:            n,
		Ready:              n,
		Updated:            n,
		Available:          n,
		ObservedGeneration: 1,
		Generation:         1,
	}
	rs.Phase = rolloutPhase(rs)
	rs.Health = deriveHealth(rs)
	return rs, nil
}

// BackupDatabase records an honest in-memory backup for the database and returns
// its descriptor. It does not run a dump (no cluster) — it records that a backup
// was requested, with phase "Succeeded" so platform/handler tests can assert the
// backup history surface. A release that was never applied errors (no fake
// success for a non-existent database).
func (f *FakeBackend) BackupDatabase(_ context.Context, spec BackupSpec) (DatabaseBackup, error) {
	if dbEngine(spec.Engine) == "" {
		return DatabaseBackup{}, fmt.Errorf("kube(fake): unsupported backup engine %q", spec.Engine)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	k := key(spec.Namespace, spec.Release)
	if _, ok := f.Applied[k]; !ok {
		return DatabaseBackup{}, fmt.Errorf("kube(fake): no release %q in %q", spec.Release, spec.Namespace)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	b := DatabaseBackup{
		Name:        fmt.Sprintf("bkp-%s-%d", sanitize(spec.Release), len(f.Backups[k])+1),
		Phase:       "Succeeded",
		CreatedAt:   now,
		CompletedAt: now,
		Engine:      dbEngine(spec.Engine),
	}
	f.Backups[k] = append(f.Backups[k], b)
	return b, nil
}

// EnsureBackupSchedule records (or, for an empty schedule, clears) the backup
// schedule for the database.
func (f *FakeBackend) EnsureBackupSchedule(_ context.Context, spec BackupSpec) error {
	if dbEngine(spec.Engine) == "" {
		return fmt.Errorf("kube(fake): unsupported backup engine %q", spec.Engine)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	k := key(spec.Namespace, spec.Release)
	if strings.TrimSpace(spec.Schedule) == "" {
		delete(f.BackupSchedules, k)
		return nil
	}
	f.BackupSchedules[k] = spec.Schedule
	return nil
}

// ListDatabaseBackups returns the recorded backups for the database, newest
// first. An unknown release returns an empty slice (no error) — the same
// "no backups yet" shape the real backend returns.
func (f *FakeBackend) ListDatabaseBackups(_ context.Context, namespace, release string) ([]DatabaseBackup, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	recorded := f.Backups[key(namespace, release)]
	out := make([]DatabaseBackup, 0, len(recorded))
	for i := len(recorded) - 1; i >= 0; i-- {
		out = append(out, recorded[i])
	}
	return out, nil
}

// RestoreDatabase records a restore request for the named backup. It errors when
// the backup name is unknown for the release (no fake-success restore of a
// non-existent backup).
func (f *FakeBackend) RestoreDatabase(_ context.Context, spec RestoreSpec) error {
	if dbEngine(spec.Engine) == "" {
		return fmt.Errorf("kube(fake): unsupported restore engine %q", spec.Engine)
	}
	if strings.TrimSpace(spec.BackupName) == "" {
		return fmt.Errorf("kube(fake): restore requires a backup name")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	k := key(spec.Namespace, spec.Release)
	found := false
	for _, b := range f.Backups[k] {
		if b.Name == spec.BackupName {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("kube(fake): no backup %q for release %q in %q", spec.BackupName, spec.Release, spec.Namespace)
	}
	f.Restores[k] = append(f.Restores[k], spec.BackupName)
	return nil
}
