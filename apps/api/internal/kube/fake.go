package kube

import (
	"context"
	"fmt"
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
	// LogLines is the canned log output returned by Logs.
	LogLines string
}

var _ Backend = (*FakeBackend)(nil)

// NewFakeBackend returns an initialized FakeBackend.
func NewFakeBackend() *FakeBackend {
	return &FakeBackend{
		BaseDomain: "vortex.v60ai.com",
		Tenants:    map[string]Quota{},
		Applied:    map[string]Workload{},
		Hosts:      map[string]string{},
		Replicas:   map[string]int{},
		LogLines:   "fake log line\n",
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
