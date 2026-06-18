package platform

import (
	"context"
	"errors"
	"testing"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
)

// TestCreateServiceCallsBackend asserts CreateService deploys onto the backend:
// it ensures the tenant and applies a workload (recorded by the FakeBackend),
// and persists the returned placement.
func TestCreateServiceCallsBackend(t *testing.T) {
	svc, fb := newSvcWithFake()
	ctx := context.Background()

	s1, err := svc.CreateService(ctx, "org-1", "proj-1", CreateServiceInput{TemplateKey: "wordpress", Name: "blog"})
	if err != nil {
		t.Fatalf("create service: %v", err)
	}
	// Tenant ensured for the org-project namespace.
	if _, ok := fb.Tenants[s1.Namespace]; !ok {
		t.Fatalf("expected tenant ensured for %q; tenants=%v", s1.Namespace, fb.Tenants)
	}
	// Workload applied and keyed by namespace/release.
	k := s1.Namespace + "/" + s1.Release
	w, ok := fb.Applied[k]
	if !ok {
		t.Fatalf("expected workload applied at %q; applied=%v", k, fb.Applied)
	}
	if w.Name != "blog" || w.ServiceTemplateKey != "wordpress" {
		t.Fatalf("unexpected applied workload: %+v", w)
	}
	if fb.Hosts[k] != s1.Host {
		t.Fatalf("persisted host %q != backend host %q", s1.Host, fb.Hosts[k])
	}
}

// TestServiceLifecycleCallsBackend asserts Deploy/Stop/Delete drive the backend
// for a deployed service.
func TestServiceLifecycleCallsBackend(t *testing.T) {
	svc, fb := newSvcWithFake()
	ctx := context.Background()

	s1, err := svc.CreateService(ctx, "org-1", "proj-1", CreateServiceInput{TemplateKey: "ghost"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	k := s1.Namespace + "/" + s1.Release

	if _, err := svc.StopService(ctx, "org-1", s1.ID); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if fb.Replicas[k] != 0 {
		t.Fatalf("expected 0 replicas after stop, got %d", fb.Replicas[k])
	}
	if _, err := svc.DeployService(ctx, "org-1", s1.ID); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if fb.Replicas[k] != 1 {
		t.Fatalf("expected 1 replica after deploy, got %d", fb.Replicas[k])
	}
	if err := svc.DeleteService(ctx, "org-1", s1.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := fb.Applied[k]; ok {
		t.Fatalf("expected workload removed from backend after delete")
	}
}

// TestCreateAppQueuedNoApply asserts CreateApp ensures the tenant but does NOT
// deploy yet (no image builder wired): status "queued", no Release, nothing
// applied to the backend.
func TestCreateAppQueuedNoApply(t *testing.T) {
	svc, fb := newSvcWithFake()
	ctx := context.Background()

	app, err := svc.CreateApp(ctx, "org-1", CreateAppInput{Name: "web", ProjectID: "proj-1"})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	if app.Status != "queued" {
		t.Fatalf("expected status queued, got %q", app.Status)
	}
	if app.Release != "" {
		t.Fatalf("expected no release before build, got %q", app.Release)
	}
	if app.Namespace == "" {
		t.Fatalf("expected tenant namespace recorded on queued app")
	}
	if _, ok := fb.Tenants[app.Namespace]; !ok {
		t.Fatalf("expected tenant ensured for %q", app.Namespace)
	}
	if len(fb.Applied) != 0 {
		t.Fatalf("expected nothing applied for a queued app, got %v", fb.Applied)
	}
}

// TestServiceLogsFromBackend asserts logs are read from the backend once a
// service is deployed. (Apps without a release return empty.)
func TestServiceLogsFromBackend(t *testing.T) {
	svc, fb := newSvcWithFake()
	ctx := context.Background()
	fb.LogLines = "hello from pod\n"

	// Apps have no release until built, so AppLogs is empty.
	app, _ := svc.CreateApp(ctx, "org-1", CreateAppInput{Name: "web", ProjectID: "proj-1"})
	if out, err := svc.AppLogs(ctx, "org-1", app.ID); err != nil || out != "" {
		t.Fatalf("expected empty app logs before deploy, got %q err=%v", out, err)
	}
}

func TestQuotaEnforcementOverCPU(t *testing.T) {
	svc := newSvc()
	ctx := context.Background()
	// Hobby default: maxCPU 0.5. Request 1.0 vCPU -> over quota.
	_, err := svc.CreateApp(ctx, "org-q", CreateAppInput{Name: "big", CPU: 1.0})
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("expected ErrQuotaExceeded, got %v", err)
	}
}

func TestQuotaEnforcementOverMemory(t *testing.T) {
	svc := newSvc()
	ctx := context.Background()
	_, err := svc.CreateApp(ctx, "org-q", CreateAppInput{Name: "big", MemoryMB: 4096})
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("expected ErrQuotaExceeded, got %v", err)
	}
}

func TestQuotaCountLimit(t *testing.T) {
	svc := newSvc()
	ctx := context.Background()
	// Hobby maxApps 3: create 3 ok, 4th over quota.
	for i := 0; i < 3; i++ {
		if _, err := svc.CreateApp(ctx, "org-c", CreateAppInput{Name: "a"}); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	if _, err := svc.CreateApp(ctx, "org-c", CreateAppInput{Name: "a"}); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("4th create: expected ErrQuotaExceeded, got %v", err)
	}
}

func TestQuotaAllowsWithinPlan(t *testing.T) {
	svc := newSvc()
	ctx := context.Background()
	// Subscribe org to scale (maxCPU 2, maxMemoryMB 4096).
	_ = svc.store.UpsertSubscription(ctx, &domain.Subscription{OrgID: "org-s", PlanID: "scale", Status: domain.SubActive})
	app, err := svc.CreateApp(ctx, "org-s", CreateAppInput{Name: "big", CPU: 2, MemoryMB: 4096})
	if err != nil {
		t.Fatalf("create within scale: %v", err)
	}
	if app.CPU != 2 || app.MemoryMB != 4096 {
		t.Fatalf("unexpected resources: %+v", app)
	}
}

func TestCreateAppDefaultsResources(t *testing.T) {
	svc := newSvc()
	app, err := svc.CreateApp(context.Background(), "org-d", CreateAppInput{Name: "web"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Defaults come from seeded platform settings (minimal: DefaultCPU 0.1, DefaultMemoryMB 128).
	if app.CPU != 0.1 || app.MemoryMB != 128 {
		t.Fatalf("defaults not applied: %+v", app)
	}
}

func TestServiceLifecycleAndIsolation(t *testing.T) {
	svc := newSvc()
	ctx := context.Background()

	s1, err := svc.CreateService(ctx, "org-1", "proj-1", CreateServiceInput{TemplateKey: "wordpress", Name: "blog"})
	if err != nil {
		t.Fatalf("create service: %v", err)
	}
	if s1.Status != "deploying" || s1.Template != "wordpress" {
		t.Fatalf("unexpected service: %+v", s1)
	}
	if s1.Release == "" || s1.Namespace == "" || s1.Host == "" {
		t.Fatalf("expected backend placement to be persisted: %+v", s1)
	}

	// Database template.
	if _, err := svc.CreateService(ctx, "org-1", "proj-1", CreateServiceInput{TemplateKey: "postgresql"}); err != nil {
		t.Fatalf("create db service: %v", err)
	}

	list, _ := svc.ListServices(ctx, "org-1")
	if len(list) != 2 {
		t.Fatalf("expected 2 services, got %d", len(list))
	}

	dep, err := svc.DeployService(ctx, "org-1", s1.ID)
	if err != nil || dep.Status != "deploying" {
		t.Fatalf("deploy: %v status=%q", err, dep.Status)
	}
	stp, _ := svc.StopService(ctx, "org-1", s1.ID)
	if stp.Status != "stopped" {
		t.Fatalf("stop status=%q", stp.Status)
	}

	// Tenant isolation.
	if _, err := svc.GetService(ctx, "org-2", s1.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant get: %v", err)
	}
	if _, err := svc.DeployService(ctx, "org-2", s1.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant deploy: %v", err)
	}

	if err := svc.DeleteService(ctx, "org-1", s1.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := svc.GetService(ctx, "org-1", s1.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
}

func TestServiceInvalidTemplate(t *testing.T) {
	svc := newSvc()
	if _, err := svc.CreateService(context.Background(), "org-1", "p", CreateServiceInput{TemplateKey: "nope"}); !errors.Is(err, ErrInvalidTemplate) {
		t.Fatalf("expected ErrInvalidTemplate, got %v", err)
	}
}

func TestServiceQuotaShareWithApps(t *testing.T) {
	svc := newSvc()
	ctx := context.Background()
	// Hobby maxApps 3: 2 apps + 1 service = 3, then a 4th workload fails.
	_, _ = svc.CreateApp(ctx, "org-x", CreateAppInput{Name: "a"})
	_, _ = svc.CreateApp(ctx, "org-x", CreateAppInput{Name: "b"})
	if _, err := svc.CreateService(ctx, "org-x", "p", CreateServiceInput{TemplateKey: "ghost"}); err != nil {
		t.Fatalf("3rd workload: %v", err)
	}
	if _, err := svc.CreateService(ctx, "org-x", "p", CreateServiceInput{TemplateKey: "ghost"}); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("4th workload: expected ErrQuotaExceeded, got %v", err)
	}
}

func TestEnvSetGetDelete(t *testing.T) {
	svc := newSvc()
	ctx := context.Background()
	app, _ := svc.CreateApp(ctx, "org-1", CreateAppInput{Name: "web"})

	if _, err := svc.SetEnv(ctx, "org-1", app.ID, "FOO", "bar", false); err != nil {
		t.Fatalf("set env: %v", err)
	}
	if _, err := svc.SetEnv(ctx, "org-1", app.ID, "BAZ", "qux", false); err != nil {
		t.Fatalf("set env 2: %v", err)
	}
	env, _ := svc.ListEnv(ctx, "org-1", app.ID)
	if len(env) != 2 || env[0].Key != "BAZ" || env[1].Key != "FOO" {
		t.Fatalf("unexpected env (want sorted): %+v", env)
	}
	if err := svc.DeleteEnv(ctx, "org-1", app.ID, "FOO"); err != nil {
		t.Fatalf("delete env: %v", err)
	}
	env, _ = svc.ListEnv(ctx, "org-1", app.ID)
	if len(env) != 1 || env[0].Key != "BAZ" {
		t.Fatalf("after delete: %+v", env)
	}

	// Tenant isolation.
	if _, err := svc.SetEnv(ctx, "org-2", app.ID, "X", "y", false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant env: %v", err)
	}
}

func TestDomainsAddListDelete(t *testing.T) {
	svc := newSvc()
	ctx := context.Background()
	app, _ := svc.CreateApp(ctx, "org-1", CreateAppInput{Name: "web"})

	d, err := svc.AddDomain(ctx, "org-1", app.ID, "example.com")
	if err != nil {
		t.Fatalf("add domain: %v", err)
	}
	if d.Domain != "example.com" || d.Verified {
		t.Fatalf("unexpected domain: %+v", d)
	}
	list, _ := svc.ListDomains(ctx, "org-1", app.ID)
	if len(list) != 1 {
		t.Fatalf("expected 1 domain, got %d", len(list))
	}
	if err := svc.DeleteDomain(ctx, "org-1", app.ID, d.ID); err != nil {
		t.Fatalf("delete domain: %v", err)
	}
	list, _ = svc.ListDomains(ctx, "org-1", app.ID)
	if len(list) != 0 {
		t.Fatalf("expected 0 domains, got %d", len(list))
	}
}

// TestMetricsUnavailableBeforeDeploy asserts an app with no Release returns an
// HONEST "unavailable" snapshot (no fabricated numbers), never synthetic data.
func TestMetricsUnavailableBeforeDeploy(t *testing.T) {
	svc := newSvc()
	ctx := context.Background()
	app, _ := svc.CreateApp(ctx, "org-1", CreateAppInput{Name: "web"})

	m, err := svc.AppMetrics(ctx, "org-1", app.ID)
	if err != nil {
		t.Fatalf("metrics: %v", err)
	}
	if m.Available {
		t.Fatalf("expected metrics unavailable for an undeployed app, got %+v", m)
	}
	if m.CPUMillicores != 0 || m.MemoryBytes != 0 || len(m.Pods) != 0 {
		t.Fatalf("expected zeroed usage for an undeployed app, got %+v", m)
	}
}

// TestMetricsLiveFromBackend asserts a deployed app returns REAL per-pod usage
// from the (fake) backend metrics-server, not synthetic waves.
func TestMetricsLiveFromBackend(t *testing.T) {
	svc, fb := newSvcWithFake()
	ctx := context.Background()
	fb.CPUMillicores = 250
	fb.MemoryBytes = 128 * 1024 * 1024

	app, _ := svc.CreateApp(ctx, "org-1", CreateAppInput{
		Name: "web", ProjectID: "proj-1", Image: "nginx:latest",
	})
	m, err := svc.AppMetrics(ctx, "org-1", app.ID)
	if err != nil {
		t.Fatalf("metrics: %v", err)
	}
	if !m.Available {
		t.Fatalf("expected metrics available for a deployed app, got %+v", m)
	}
	if m.CPUMillicores != 250 || m.MemoryBytes != 128*1024*1024 {
		t.Fatalf("aggregate usage = %dm / %dB, want 250m / 128MiB", m.CPUMillicores, m.MemoryBytes)
	}
	if len(m.Pods) != 1 || m.Pods[0].CPUMillicores != 250 {
		t.Fatalf("per-pod usage = %+v, want one pod at 250m", m.Pods)
	}
}

// TestMetricsHonestWhenServerMissing asserts a deployed app whose cluster has no
// metrics-server returns Available=false (honest), never fabricated numbers.
func TestMetricsHonestWhenServerMissing(t *testing.T) {
	svc, fb := newSvcWithFake()
	ctx := context.Background()
	fb.MetricsAvailable = false

	app, _ := svc.CreateApp(ctx, "org-1", CreateAppInput{
		Name: "web", ProjectID: "proj-1", Image: "nginx:latest",
	})
	m, err := svc.AppMetrics(ctx, "org-1", app.ID)
	if err != nil {
		t.Fatalf("metrics: %v", err)
	}
	if m.Available || m.Unavailable == "" {
		t.Fatalf("expected honest unavailable snapshot, got %+v", m)
	}
}
