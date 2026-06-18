package platform

import (
	"context"
	"errors"
	"testing"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
)

// TestDeployReAppliesWithEnvAndDomains asserts Deploy re-renders the workload
// with the app's stored env + custom domains and re-applies it (helm upgrade),
// not just a scale-up.
func TestDeployReAppliesWithEnvAndDomains(t *testing.T) {
	svc, fb := newSvcWithFake()
	ctx := context.Background()

	app, err := svc.CreateApp(ctx, "org-1", CreateAppInput{Name: "web", Image: "nginx:1.27"})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}

	// Set env + a custom domain after create.
	if err := svc.store.SetAppEnv(ctx, app.ID, "FOO", "bar", false); err != nil {
		t.Fatalf("set env: %v", err)
	}
	// Only VERIFIED custom domains are routed (added to the workload hostnames), so
	// the domain is created already-verified here.
	if err := svc.store.CreateDomain(ctx, &domain.Domain{
		ID: "d1", OrgID: "org-1", AppID: app.ID, Domain: "www.example.com",
		Status: domain.DomainVerified, Verified: true,
	}); err != nil {
		t.Fatalf("create domain: %v", err)
	}

	dep, err := svc.Deploy(ctx, "org-1", app.ID)
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if dep.Status != "deploying" {
		t.Fatalf("status = %q, want deploying", dep.Status)
	}

	k := dep.Namespace + "/" + dep.Release
	w, ok := fb.Applied[k]
	if !ok {
		t.Fatalf("expected workload re-applied at %q", k)
	}
	if w.Env["FOO"] != "bar" {
		t.Errorf("workload env FOO = %q, want bar (env=%v)", w.Env["FOO"], w.Env)
	}
	found := false
	for _, d := range w.Domains {
		if d == "www.example.com" {
			found = true
		}
	}
	if !found {
		t.Errorf("workload domains = %v, want to include www.example.com", w.Domains)
	}
	if w.Image != "nginx:1.27" {
		t.Errorf("workload image = %q, want nginx:1.27", w.Image)
	}
}

// TestDeployNoImageReturnsErrNoImage asserts a NON-git app with no image cannot
// be deployed (no fake success) and returns ErrNoImage. (Git apps rebuild on
// deploy instead — see wave2_test.go.)
func TestDeployNoImageReturnsErrNoImage(t *testing.T) {
	svc := newSvc()
	ctx := context.Background()

	app, err := svc.CreateApp(ctx, "org-1", CreateAppInput{Name: "web"})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	if app.Status != "queued" {
		t.Fatalf("status = %q, want queued", app.Status)
	}
	if _, err := svc.Deploy(ctx, "org-1", app.ID); !errors.Is(err, ErrNoImage) {
		t.Fatalf("Deploy: expected ErrNoImage, got %v", err)
	}
}

// TestDatabasesCountTowardQuota asserts databases consume the MaxApps budget so
// the plan limit is enforced for DBs too.
func TestDatabasesCountTowardQuota(t *testing.T) {
	svc := newSvc()
	ctx := context.Background()

	// Default plan MaxApps from the seeded store; create databases until quota is
	// hit, then assert the next create is rejected with ErrQuotaExceeded.
	lim := svc.planLimits(ctx, "org-1")
	if lim.MaxApps <= 0 {
		t.Skipf("default plan MaxApps not positive (%d)", lim.MaxApps)
	}
	for i := 0; i < lim.MaxApps; i++ {
		_, err := svc.CreateDatabase(ctx, "org-1", CreateDatabaseInput{Name: "db", Engine: "postgresql"})
		if err != nil {
			t.Fatalf("create database %d: %v", i, err)
		}
	}
	if _, err := svc.CreateApp(ctx, "org-1", CreateAppInput{Name: "over", Image: "nginx:1"}); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("expected ErrQuotaExceeded once DBs fill the workload budget, got %v", err)
	}
}
