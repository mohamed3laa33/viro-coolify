package platform

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/billing"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/build"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/kube"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/store"
)

// newGitSvc builds a platform service with an injected FakeBuilder and a build
// WaitGroup so tests can deterministically wait for the async build to finish.
func newGitSvc(t *testing.T, fb *build.FakeBuilder) (*Service, *kube.FakeBackend, *sync.WaitGroup) {
	t.Helper()
	st := store.NewMemoryStore()
	kb := kube.NewFakeBackend()
	var wg sync.WaitGroup
	svc := NewService(st, kb, billing.NewService(st, nil),
		WithBuilder(fb),
		WithBuildRegistry("ghcr.io/acme"),
		WithBuildWaitGroup(&wg),
	)
	return svc, kb, &wg
}

// TestGitAppCreateBuildsAndDeploys asserts a git-source app records a Build,
// runs the builder, sets the app image from the build result, and deploys it.
func TestGitAppCreateBuildsAndDeploys(t *testing.T) {
	fb := build.NewFakeBuilder()
	fb.ImageOverride = "ghcr.io/acme/built-image:sha"
	svc, kbe, wg := newGitSvc(t, fb)
	ctx := context.Background()

	app, err := svc.CreateApp(ctx, "org-1", CreateAppInput{
		Name: "web", GitRepository: "https://github.com/acme/web.git", GitBranch: "main",
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	if app.Status != "building" {
		t.Fatalf("status on create = %q, want building", app.Status)
	}

	wg.Wait() // let the async build + deploy finish

	// The builder was invoked with a sane request.
	calls := fb.Calls()
	if len(calls) != 1 {
		t.Fatalf("builder calls = %d, want 1", len(calls))
	}
	if calls[0].GitRepo != "https://github.com/acme/web.git" || calls[0].GitRef != "main" {
		t.Fatalf("unexpected build request: %+v", calls[0])
	}
	if calls[0].ImageRef == "" {
		t.Fatalf("build request has no ImageRef")
	}

	// The app now carries the builder's image, a release, and is deploying.
	got, err := svc.GetApp(ctx, "org-1", app.ID)
	if err != nil {
		t.Fatalf("get app: %v", err)
	}
	if got.Image != "ghcr.io/acme/built-image:sha" {
		t.Fatalf("app image = %q, want the build result", got.Image)
	}
	if got.Release == "" {
		t.Fatalf("expected a release after build+deploy, got none")
	}
	if got.Status != "deploying" {
		t.Fatalf("status after build = %q, want deploying", got.Status)
	}

	// The image was actually applied to the backend.
	w, ok := kbe.Applied[got.Namespace+"/"+got.Release]
	if !ok {
		t.Fatalf("expected workload applied for built app")
	}
	if w.Image != "ghcr.io/acme/built-image:sha" {
		t.Fatalf("applied image = %q, want the build result", w.Image)
	}

	// A succeeded Build record exists with the produced image.
	builds, err := svc.ListBuilds(ctx, "org-1", app.ID)
	if err != nil {
		t.Fatalf("list builds: %v", err)
	}
	if len(builds) != 1 {
		t.Fatalf("builds = %d, want 1", len(builds))
	}
	if builds[0].Status != "succeeded" || builds[0].Image != "ghcr.io/acme/built-image:sha" {
		t.Fatalf("unexpected build: %+v", builds[0])
	}
	if builds[0].FinishedAt.IsZero() {
		t.Fatalf("succeeded build should have FinishedAt set")
	}
}

// TestGitAppBuildFailureMarksAppFailed asserts a build failure marks the app
// "build_failed" and the Build "failed" with logs, and never deploys.
func TestGitAppBuildFailureMarksAppFailed(t *testing.T) {
	fb := build.NewFakeBuilder()
	fb.Err = errors.New("docker build exited 1\nstep 3 failed")
	svc, kbe, wg := newGitSvc(t, fb)
	ctx := context.Background()

	app, err := svc.CreateApp(ctx, "org-1", CreateAppInput{
		Name: "web", GitRepository: "https://github.com/acme/web.git",
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	wg.Wait()

	got, err := svc.GetApp(ctx, "org-1", app.ID)
	if err != nil {
		t.Fatalf("get app: %v", err)
	}
	if got.Status != "build_failed" {
		t.Fatalf("status = %q, want build_failed", got.Status)
	}
	if got.Image != "" || got.Release != "" {
		t.Fatalf("failed build must not set image/release: %+v", got)
	}
	if len(kbe.Applied) != 0 {
		t.Fatalf("failed build must not deploy, got %d applies", len(kbe.Applied))
	}

	builds, err := svc.ListBuilds(ctx, "org-1", app.ID)
	if err != nil {
		t.Fatalf("list builds: %v", err)
	}
	if len(builds) != 1 || builds[0].Status != "failed" {
		t.Fatalf("unexpected builds: %+v", builds)
	}
	if builds[0].Logs == "" {
		t.Fatalf("failed build should capture logs")
	}
}

// TestDeployGitAppRebuilds asserts POST /deploy on a git app with no image
// re-triggers a build (a new Build record) rather than returning ErrNoImage.
func TestDeployGitAppRebuilds(t *testing.T) {
	fb := build.NewFakeBuilder()
	fb.Err = errors.New("first build fails") // keep app image-less for the rebuild assertion
	svc, _, wg := newGitSvc(t, fb)
	ctx := context.Background()

	app, err := svc.CreateApp(ctx, "org-1", CreateAppInput{
		Name: "web", GitRepository: "https://github.com/acme/web.git",
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	wg.Wait()

	if _, err := svc.Deploy(ctx, "org-1", app.ID); err != nil {
		t.Fatalf("deploy (rebuild) should not error, got %v", err)
	}
	wg.Wait()

	builds, err := svc.ListBuilds(ctx, "org-1", app.ID)
	if err != nil {
		t.Fatalf("list builds: %v", err)
	}
	if len(builds) != 2 {
		t.Fatalf("expected 2 builds after create+redeploy, got %d", len(builds))
	}
}

// TestGetBuildScopedToOrg asserts a build cannot be read across tenants.
func TestGetBuildScopedToOrg(t *testing.T) {
	fb := build.NewFakeBuilder()
	svc, _, wg := newGitSvc(t, fb)
	ctx := context.Background()

	app, err := svc.CreateApp(ctx, "org-1", CreateAppInput{
		Name: "web", GitRepository: "https://github.com/acme/web.git",
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	wg.Wait()

	builds, _ := svc.ListBuilds(ctx, "org-1", app.ID)
	if len(builds) != 1 {
		t.Fatalf("builds = %d, want 1", len(builds))
	}
	// Cross-tenant get is hidden as not-found.
	if _, err := svc.GetBuild(ctx, "org-2", app.ID, builds[0].ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant GetBuild: expected ErrNotFound, got %v", err)
	}
	// Same-tenant get works and includes status.
	b, err := svc.GetBuild(ctx, "org-1", app.ID, builds[0].ID)
	if err != nil {
		t.Fatalf("GetBuild: %v", err)
	}
	if b.AppID != app.ID {
		t.Fatalf("unexpected build: %+v", b)
	}
}
