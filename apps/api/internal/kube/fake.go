package kube

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
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
		BaseDomain:  "vortex.v60ai.com",
		Tenants:     map[string]Quota{},
		Applied:     map[string]Workload{},
		Hosts:       map[string]string{},
		Replicas:    map[string]int{},
		PullSecrets: map[string]bool{},
		AppSecrets:  map[string]map[string]string{},
		LogLines:    "fake log line\n",
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

func (f *FakeBackend) Status(_ context.Context, namespace, release string) (Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := key(namespace, release)
	if _, ok := f.Applied[k]; !ok {
		return Status{Phase: "Unknown"}, fmt.Errorf("kube(fake): no release %q in %q", release, namespace)
	}
	n := f.Replicas[k]
	phase := "Running"
	if n == 0 {
		phase = "Scaled to zero"
	}
	return Status{Phase: phase, Replicas: n, ReadyReplicas: n}, nil
}
