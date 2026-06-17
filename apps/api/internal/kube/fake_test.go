package kube

import (
	"context"
	"testing"
)

// FakeBackend must satisfy the Backend contract (compile-time check is in fake.go).
func TestFakeBackendLifecycle(t *testing.T) {
	f := NewFakeBackend()
	ctx := context.Background()

	ns, err := f.EnsureTenant(ctx, "acme", "web", Quota{MaxCPU: 4, MaxMemoryMB: 8192, MaxApps: 5})
	if err != nil {
		t.Fatalf("EnsureTenant: %v", err)
	}
	if ns != "vortex-acme-web" {
		t.Fatalf("namespace = %q, want vortex-acme-web", ns)
	}
	if _, ok := f.Tenants[ns]; !ok {
		t.Error("tenant not recorded")
	}

	rel, host, err := f.Apply(ctx, Workload{OrgSlug: "acme", ProjectSlug: "web", Name: "api", Kind: "app"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if rel != "api" {
		t.Errorf("release = %q, want api", rel)
	}
	if host != "api.web.acme.vortex.v60ai.com" {
		t.Errorf("host = %q, want api.web.acme.vortex.v60ai.com", host)
	}

	st, err := f.Status(ctx, ns, rel)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Phase != "Running" || st.Replicas != 1 {
		t.Errorf("status = %+v, want Running 1", st)
	}

	if err := f.Stop(ctx, ns, rel); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	st, _ = f.Status(ctx, ns, rel)
	if st.Phase != "Scaled to zero" || st.Replicas != 0 {
		t.Errorf("status after Stop = %+v, want Scaled to zero 0", st)
	}

	if err := f.Start(ctx, ns, rel); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := f.Restart(ctx, ns, rel); err != nil {
		t.Fatalf("Restart: %v", err)
	}

	if _, err := f.Logs(ctx, ns, rel, 50); err != nil {
		t.Fatalf("Logs: %v", err)
	}

	if err := f.Delete(ctx, ns, rel); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := f.Status(ctx, ns, rel); err == nil {
		t.Error("Status after Delete should error")
	}
}
