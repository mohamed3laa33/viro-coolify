package platform

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/billing"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/build"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/kube"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/store"
)

// hookBuilder is a Builder that runs a hook while the build is "in flight" (before
// returning the result), letting a test mutate app state mid-build.
type hookBuilder struct {
	image string
	hook  func()
}

func (h *hookBuilder) Build(_ context.Context, r build.Request) (build.Result, error) {
	if h.hook != nil {
		h.hook()
	}
	img := r.ImageRef
	if h.image != "" {
		img = h.image
	}
	return build.Result{Image: img}, nil
}

// TestBuiltAppGetsImagePullSecret asserts a git-built app deploys with the
// tenant-namespace imagePullSecret attached (and EnsureImagePullSecret was called),
// so a private built image can be pulled.
func TestBuiltAppGetsImagePullSecret(t *testing.T) {
	st := store.NewMemoryStore()
	kb := kube.NewFakeBackend()
	var wg sync.WaitGroup
	fb := build.NewFakeBuilder()
	fb.ImageOverride = "ghcr.io/acme/o/p/a:tag"
	svc := NewService(st, kb, billing.NewService(st, nil),
		WithBuilder(fb),
		WithBuildRegistry("ghcr.io/acme"),
		WithPullSecretName("vortex-registry-pull"),
		WithBuildWaitGroup(&wg),
	)
	ctx := context.Background()

	app, err := svc.CreateApp(ctx, "org-1", CreateAppInput{
		Name: "web", GitRepository: "https://github.com/acme/web.git",
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	wg.Wait()

	got, _ := svc.GetApp(ctx, "org-1", app.ID)
	w, ok := kb.Applied[got.Namespace+"/"+got.Release]
	if !ok {
		t.Fatalf("expected workload applied")
	}
	if w.ImagePullSecret != "vortex-registry-pull" {
		t.Fatalf("workload ImagePullSecret = %q, want vortex-registry-pull", w.ImagePullSecret)
	}
	if !kb.PullSecrets[got.Namespace+"/vortex-registry-pull"] {
		t.Fatalf("EnsureImagePullSecret was not called for the tenant namespace")
	}
}

// TestUserStoppedMidBuildNotResurrected asserts that if the user stops the app
// while the build is running, a successful build does NOT redeploy/resurrect it:
// the app stays "stopped" and is never applied to the backend.
func TestUserStoppedMidBuildNotResurrected(t *testing.T) {
	st := store.NewMemoryStore()
	kb := kube.NewFakeBackend()
	var wg sync.WaitGroup

	// The hook flips the (sole) building app to "stopped" mid-build by listing the
	// org's apps from the store — avoiding a data race on a shared appID variable
	// (the build goroutine starts before CreateApp returns).
	hb := &hookBuilder{
		image: "ghcr.io/acme/o/p/a:tag",
		hook: func() {
			apps, err := st.ListAppsByOrg(context.Background(), "org-1")
			if err != nil || len(apps) == 0 {
				return
			}
			a := apps[0]
			a.Status = "stopped"
			_ = st.UpdateApp(context.Background(), &a)
		},
	}
	svc := NewService(st, kb, billing.NewService(st, nil),
		WithBuilder(hb),
		WithBuildRegistry("ghcr.io/acme"),
		WithBuildWaitGroup(&wg),
	)
	ctx := context.Background()

	app, err := svc.CreateApp(ctx, "org-1", CreateAppInput{
		Name: "web", GitRepository: "https://github.com/acme/web.git",
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	wg.Wait()

	got, _ := svc.GetApp(ctx, "org-1", app.ID)
	if got.Status != "stopped" {
		t.Fatalf("status = %q, want stopped (not resurrected)", got.Status)
	}
	if got.Release != "" {
		t.Fatalf("stopped app must not be deployed, got release %q", got.Release)
	}
	if len(kb.Applied) != 0 {
		t.Fatalf("stopped-mid-build app must not be applied, got %d applies", len(kb.Applied))
	}
	// The build still recorded success with the produced image.
	builds, _ := svc.ListBuilds(ctx, "org-1", app.ID, store.Page{})
	if len(builds) != 1 || builds[0].Status != "succeeded" {
		t.Fatalf("expected one succeeded build, got %+v", builds)
	}
}

// TestImageRefCollisionFree asserts the computed image path is built from
// slash-delimited tenant IDs so two tenants whose sanitized slugs would collapse
// to the same string still get DISTINCT image paths.
func TestImageRefCollisionFree(t *testing.T) {
	svc := &Service{buildRegistry: "ghcr.io/acme"}

	// Two distinct orgs that would collapse to the same slug under separator
	// concatenation ("a-b" + "c" == "a" + "b-c").
	r1 := svc.imageRef("a-b", "proj", "c", "tag")
	r2 := svc.imageRef("a", "proj", "b-c", "tag")
	if r1 == r2 {
		t.Fatalf("image refs collide: %q == %q", r1, r2)
	}
	// Path uses slash-delimited IDs.
	if !strings.HasPrefix(r1, "ghcr.io/acme/a-b/proj/c:") {
		t.Fatalf("unexpected image ref %q", r1)
	}
	// Empty project falls back to "default" rather than collapsing.
	if got := svc.imageRef("org", "", "app", "tag"); got != "ghcr.io/acme/org/default/app:tag" {
		t.Fatalf("empty-project image ref = %q", got)
	}
}
