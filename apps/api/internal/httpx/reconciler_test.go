package httpx

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/config"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/kube"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/platform"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/store"
)

func newReconcileServer(t *testing.T) (*Server, *kube.FakeBackend, store.Store) {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	st := store.NewMemoryStore()
	fb := kube.NewFakeBackend()
	s := NewServer(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), st, WithBackend(fb))
	return s, fb, st
}

func TestReconcileWritesBackRunningStatus(t *testing.T) {
	s, fb, st := newReconcileServer(t)
	ctx := context.Background()

	if err := st.CreateOrganization(ctx, &domain.Organization{ID: "org-1", Name: "Acme", Slug: "acme"}); err != nil {
		t.Fatalf("create org: %v", err)
	}

	// Deploy a workload on the backend so Status reports it as running (replicas 1).
	rel, host, err := fb.Apply(ctx, kube.Workload{
		OrgSlug: "acme", ProjectSlug: "web", Name: "api", Kind: "app", Image: "nginx:1",
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}

	// Seed an app with a stale status and the matching placement.
	app := &domain.App{
		ID: "a1", OrgID: "org-1", Name: "api", Image: "nginx:1",
		Status: "deploying", Namespace: "vortex-acme-web", Release: rel, Host: host,
	}
	if err := st.CreateApp(ctx, app); err != nil {
		t.Fatalf("create app: %v", err)
	}

	s.reconcileOnce(ctx)

	got, err := st.GetApp(ctx, "a1")
	if err != nil {
		t.Fatalf("get app: %v", err)
	}
	if got.Status != "running" {
		t.Fatalf("reconciled status = %q, want running", got.Status)
	}
}

func TestReconcileScaledToZero(t *testing.T) {
	s, fb, st := newReconcileServer(t)
	ctx := context.Background()
	if err := st.CreateOrganization(ctx, &domain.Organization{ID: "org-1", Slug: "acme"}); err != nil {
		t.Fatalf("create org: %v", err)
	}
	rel, host, _ := fb.Apply(ctx, kube.Workload{OrgSlug: "acme", ProjectSlug: "web", Name: "api", Kind: "app"})
	if err := fb.Stop(ctx, "vortex-acme-web", rel); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if err := st.CreateApp(ctx, &domain.App{
		ID: "a1", OrgID: "org-1", Name: "api", Status: "running",
		Namespace: "vortex-acme-web", Release: rel, Host: host,
	}); err != nil {
		t.Fatalf("create app: %v", err)
	}

	s.reconcileOnce(ctx)

	got, _ := st.GetApp(ctx, "a1")
	if got.Status != "scaled-to-zero" {
		t.Fatalf("status = %q, want scaled-to-zero", got.Status)
	}
}

// TestReconcileDoesNotClobberStopped verifies a user-initiated Stop is sticky:
// after platform.Stop() the workload is "stopped" and a reconcile pass MUST leave
// it that way (otherwise it flips to scaled-to-zero and billing resumes charging).
func TestReconcileDoesNotClobberStopped(t *testing.T) {
	s, _, st := newReconcileServer(t)
	ctx := context.Background()

	if err := st.CreateOrganization(ctx, &domain.Organization{ID: "org-1", Name: "Acme", Slug: "acme"}); err != nil {
		t.Fatalf("create org: %v", err)
	}

	// Seed a non-zero vCPU price so the billing gate (billable) is observable: a
	// running workload yields a positive estimate, a stopped one yields 0.
	if err := st.UpsertPricingComponent(ctx, &domain.PricingComponent{
		Key: "cpu", Name: "vCPU", Unit: "vCPU-hour", PricePerHour: 1.0,
		Currency: "usd", Active: true, SortOrder: 1,
	}); err != nil {
		t.Fatalf("seed pricing: %v", err)
	}

	// Create an image-based app so it deploys (gets a Release) on the fake backend.
	// Size stays within the default plan quota (max 0.5 vCPU).
	app, err := s.platform.CreateApp(ctx, "org-1", platform.CreateAppInput{
		Name: "api", Image: "nginx:1", CPU: 0.25, MemoryMB: 256,
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	if app.Release == "" {
		t.Fatalf("expected a Release after deploy, got empty")
	}

	// While running, billing must consider the workload billable (non-zero estimate).
	if est, err := s.billing.OrgMonthlyEstimateCents(ctx, "org-1"); err != nil || est <= 0 {
		t.Fatalf("running estimate = %d (err %v), want > 0", est, err)
	}

	// User stops the app: status becomes "stopped".
	stopped, err := s.platform.Stop(ctx, "org-1", app.ID)
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if stopped.Status != "stopped" {
		t.Fatalf("status after Stop = %q, want stopped", stopped.Status)
	}

	// The next reconcile tick must NOT overwrite "stopped" with scaled-to-zero.
	s.reconcileOnce(ctx)

	got, err := st.GetApp(ctx, app.ID)
	if err != nil {
		t.Fatalf("get app: %v", err)
	}
	if got.Status != "stopped" {
		t.Fatalf("status after reconcile = %q, want stopped (sticky)", got.Status)
	}

	// And billing must stay off for the stopped workload (estimate back to 0).
	if est, err := s.billing.OrgMonthlyEstimateCents(ctx, "org-1"); err != nil || est != 0 {
		t.Fatalf("stopped estimate = %d (err %v), want 0", est, err)
	}
}

// TestReconcileDoesNotClobberBuildFailed verifies a "build_failed" status (e.g. a
// rebuild of a still-running app, whose Deployment is non-empty and would read as
// "running") is sticky and never overwritten by the reconciler.
func TestReconcileDoesNotClobberBuildFailed(t *testing.T) {
	s, fb, st := newReconcileServer(t)
	ctx := context.Background()
	if err := st.CreateOrganization(ctx, &domain.Organization{ID: "org-1", Slug: "acme"}); err != nil {
		t.Fatalf("create org: %v", err)
	}
	// Deploy a workload so the backend reports it running (replicas 1).
	rel, host, err := fb.Apply(ctx, kube.Workload{OrgSlug: "acme", ProjectSlug: "web", Name: "api", Kind: "app"})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	// The app's last build failed, but its prior Deployment is still up.
	if err := st.CreateApp(ctx, &domain.App{
		ID: "a1", OrgID: "org-1", Name: "api", Status: "build_failed",
		Namespace: "vortex-acme-web", Release: rel, Host: host,
	}); err != nil {
		t.Fatalf("create app: %v", err)
	}

	s.reconcileOnce(ctx)

	got, _ := st.GetApp(ctx, "a1")
	if got.Status != "build_failed" {
		t.Fatalf("status after reconcile = %q, want build_failed (sticky)", got.Status)
	}
}

// TestReconcileFailedMarksCurrentReleaseFailed asserts that when the reconciler
// observes a workload as Failed and writes the app's status, it ALSO transitions
// the app's current release to failed — so a crashlooped deploy is recorded in the
// release history rather than staying "active" forever (fix #5).
func TestReconcileFailedMarksCurrentReleaseFailed(t *testing.T) {
	s, fb, st := newReconcileServer(t)
	ctx := context.Background()
	if err := st.CreateOrganization(ctx, &domain.Organization{ID: "org-1", Slug: "acme"}); err != nil {
		t.Fatalf("create org: %v", err)
	}

	// Create an image-based app so it deploys (records rev1 active) on the fake.
	app, err := s.platform.CreateApp(ctx, "org-1", platform.CreateAppInput{
		Name: "api", Image: "nginx:1", CPU: 0.25, MemoryMB: 256,
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	if app.Release == "" {
		t.Fatalf("expected a Release after deploy")
	}

	// The current release is active to start with.
	cur, err := s.platform.CurrentRelease(ctx, app.ID)
	if err != nil || cur == nil || cur.Status != domain.ReleaseActive {
		t.Fatalf("initial current release = %+v (err %v), want active", cur, err)
	}

	// Force the backend to report the workload as Failed (crashloop).
	fb.PhaseOverride[app.Namespace+"/"+app.Release] = "Failed"

	s.reconcileOnce(ctx)

	// The app row is now failed...
	got, _ := st.GetApp(ctx, app.ID)
	if got.Status != "failed" {
		t.Fatalf("app status after failed reconcile = %q, want failed", got.Status)
	}
	// ...and so is its current release.
	cur, err = s.platform.CurrentRelease(ctx, app.ID)
	if err != nil {
		t.Fatalf("current release: %v", err)
	}
	if cur != nil && cur.Status == domain.ReleaseActive {
		t.Fatalf("current release still active after failed reconcile")
	}
	rels, _ := st.ListReleasesByApp(ctx, app.ID, store.Page{})
	var sawFailed bool
	for _, r := range rels {
		if r.Status == domain.ReleaseFailed {
			sawFailed = true
		}
	}
	if !sawFailed {
		t.Fatalf("expected a failed release after failed reconcile: %+v", rels)
	}
}

func TestReconcileSkipsWorkloadsWithoutRelease(t *testing.T) {
	s, _, st := newReconcileServer(t)
	ctx := context.Background()
	if err := st.CreateOrganization(ctx, &domain.Organization{ID: "org-1", Slug: "acme"}); err != nil {
		t.Fatalf("create org: %v", err)
	}
	// No Release => queued app; reconciler must leave it untouched (and not error).
	if err := st.CreateApp(ctx, &domain.App{ID: "a1", OrgID: "org-1", Name: "api", Status: "queued"}); err != nil {
		t.Fatalf("create app: %v", err)
	}
	s.reconcileOnce(ctx)
	got, _ := st.GetApp(ctx, "a1")
	if got.Status != "queued" {
		t.Fatalf("status = %q, want queued (untouched)", got.Status)
	}
}

func TestStartReconcilerStopsOnContextCancel(t *testing.T) {
	s, _, _ := newReconcileServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	s.StartReconciler(ctx, 10*time.Millisecond, &wg)
	cancel()
	// The WaitGroup lets us deterministically confirm the loop exits on cancel
	// (this is the drain other code Wait()s on before closing the store).
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reconciler loop did not exit after context cancel")
	}
}
