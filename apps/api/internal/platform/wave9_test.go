package platform

import (
	"context"
	"errors"
	"testing"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/billing"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/kube"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/store"
)

func newReleaseSvc(t *testing.T) (*Service, *kube.FakeBackend, store.Store) {
	t.Helper()
	st := store.NewMemoryStore()
	fb := kube.NewFakeBackend()
	svc := NewService(st, fb, billing.NewService(st, nil), WithBaseDomain("vortex.v60ai.com"))
	return svc, fb, st
}

// TestDeployRecordsReleaseRevisions asserts a deploy records rev1, a redeploy
// records rev2 and supersedes rev1, and ListReleases returns newest-first.
func TestDeployRecordsReleaseRevisions(t *testing.T) {
	svc, _, _ := newReleaseSvc(t)
	ctx := context.Background()

	app, err := svc.CreateApp(ctx, "org-1", CreateAppInput{Name: "web", Image: "nginx:1"})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}

	rels, err := svc.ListReleases(ctx, "org-1", app.ID, store.Page{})
	if err != nil {
		t.Fatalf("list releases: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("after create: releases = %d, want 1", len(rels))
	}
	if rels[0].Revision != 1 || rels[0].Image != "nginx:1" || rels[0].Status != domain.ReleaseActive {
		t.Fatalf("rev1 = %+v", rels[0])
	}

	// Change the image and redeploy.
	if _, err := svc.UpdateApp(ctx, "org-1", app.ID, UpdateAppInput{Image: strptr("nginx:2")}); err != nil {
		t.Fatalf("update app: %v", err)
	}
	if _, err := svc.Deploy(ctx, "org-1", app.ID); err != nil {
		t.Fatalf("deploy: %v", err)
	}

	rels, err = svc.ListReleases(ctx, "org-1", app.ID, store.Page{})
	if err != nil {
		t.Fatalf("list releases: %v", err)
	}
	// rev1 (create) + rev2 (update re-apply) + rev3 (deploy).
	if len(rels) < 2 {
		t.Fatalf("after redeploy: releases = %d, want >=2", len(rels))
	}
	// Newest first.
	if rels[0].Revision <= rels[1].Revision {
		t.Fatalf("releases not ordered desc: %d then %d", rels[0].Revision, rels[1].Revision)
	}
	// Exactly one active.
	active := 0
	for _, r := range rels {
		if r.Status == domain.ReleaseActive {
			active++
		}
	}
	if active != 1 {
		t.Fatalf("active releases = %d, want 1", active)
	}
	if rels[0].Status != domain.ReleaseActive || rels[0].Image != "nginx:2" {
		t.Fatalf("top release = %+v, want active nginx:2", rels[0])
	}
}

// TestRollbackReAppliesTargetImage asserts rollback to rev1 re-Applies its image,
// records a new rolled-back-noted release, and the app row carries the old image.
func TestRollbackReAppliesTargetImage(t *testing.T) {
	svc, fb, _ := newReleaseSvc(t)
	ctx := context.Background()

	app, err := svc.CreateApp(ctx, "org-1", CreateAppInput{Name: "web", Image: "nginx:1"})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	if _, err := svc.UpdateApp(ctx, "org-1", app.ID, UpdateAppInput{Image: strptr("nginx:2")}); err != nil {
		t.Fatalf("update app: %v", err)
	}

	// Roll back to rev1 (the original nginx:1).
	rolled, err := svc.RollbackApp(ctx, "org-1", app.ID, 1)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if rolled.Image != "nginx:1" {
		t.Fatalf("app image after rollback = %q, want nginx:1", rolled.Image)
	}

	// The backend re-Applied with the rolled-back image.
	ns := "vortex-org-1-default"
	wl, ok := fb.Applied[ns+"/web"]
	if !ok {
		t.Fatalf("no applied workload for release")
	}
	if wl.Image != "nginx:1" {
		t.Fatalf("applied image = %q, want nginx:1", wl.Image)
	}

	rels, err := svc.ListReleases(ctx, "org-1", app.ID, store.Page{})
	if err != nil {
		t.Fatalf("list releases: %v", err)
	}
	top := rels[0]
	if top.Status != domain.ReleaseActive || top.Image != "nginx:1" {
		t.Fatalf("rollback release = %+v, want active nginx:1", top)
	}
	if top.Note == "" {
		t.Fatalf("rollback release should carry a note, got empty")
	}
	// The previously-active release (nginx:2) is marked rolled_back.
	var sawRolledBack bool
	for _, r := range rels {
		if r.Image == "nginx:2" && r.Status == domain.ReleaseRolledBack {
			sawRolledBack = true
		}
	}
	if !sawRolledBack {
		t.Fatalf("expected the superseded nginx:2 release marked rolled_back: %+v", rels)
	}
}

// TestRollbackDefaultPrevious asserts a body-less rollback targets the previous
// release.
func TestRollbackDefaultPrevious(t *testing.T) {
	svc, _, _ := newReleaseSvc(t)
	ctx := context.Background()

	app, _ := svc.CreateApp(ctx, "org-1", CreateAppInput{Name: "web", Image: "nginx:1"})
	_, _ = svc.UpdateApp(ctx, "org-1", app.ID, UpdateAppInput{Image: strptr("nginx:2")})

	rolled, err := svc.RollbackApp(ctx, "org-1", app.ID, 0)
	if err != nil {
		t.Fatalf("rollback default: %v", err)
	}
	if rolled.Image != "nginx:1" {
		t.Fatalf("default rollback image = %q, want nginx:1 (previous)", rolled.Image)
	}
}

// TestRollbackNoHistory asserts a rollback with no prior release errors.
func TestRollbackNoHistory(t *testing.T) {
	svc, _, _ := newReleaseSvc(t)
	ctx := context.Background()
	// An app with a single release has no PREVIOUS to roll back to.
	app, _ := svc.CreateApp(ctx, "org-1", CreateAppInput{Name: "web", Image: "nginx:1"})
	if _, err := svc.RollbackApp(ctx, "org-1", app.ID, 0); err != ErrNoRelease {
		t.Fatalf("rollback err = %v, want ErrNoRelease", err)
	}
}

// TestUpdateAppRechecksQuotaAndReApplies asserts UpdateApp changes the image,
// re-Applies, records a release, and re-checks the plan quota.
func TestUpdateAppRechecksQuotaAndReApplies(t *testing.T) {
	svc, fb, _ := newReleaseSvc(t)
	ctx := context.Background()

	app, _ := svc.CreateApp(ctx, "org-1", CreateAppInput{Name: "web", Image: "nginx:1", CPU: 0.25, MemoryMB: 128})

	// Update the image + size within the hobby plan ceiling (MaxCPU 0.5, MaxMem 512).
	updated, err := svc.UpdateApp(ctx, "org-1", app.ID, UpdateAppInput{
		Image: strptr("nginx:2"), CPU: f64ptr(0.5), MemoryMB: intptr(512),
	})
	if err != nil {
		t.Fatalf("update app: %v", err)
	}
	if updated.Image != "nginx:2" || updated.CPU != 0.5 || updated.MemoryMB != 512 {
		t.Fatalf("updated app = %+v", updated)
	}
	wl := fb.Applied["vortex-org-1-default/web"]
	if wl.Image != "nginx:2" || wl.CPU != 0.5 || wl.MemoryMB != 512 {
		t.Fatalf("re-applied workload = %+v", wl)
	}

	// A size beyond the plan ceiling is rejected (quota re-check on update).
	if _, err := svc.UpdateApp(ctx, "org-1", app.ID, UpdateAppInput{CPU: f64ptr(8)}); err == nil {
		t.Fatalf("oversized update should be rejected by quota")
	}

	// A release was recorded for the successful update.
	rels, _ := svc.ListReleases(ctx, "org-1", app.ID, store.Page{})
	if len(rels) < 2 {
		t.Fatalf("expected >=2 releases after update, got %d", len(rels))
	}
}

// TestScaleAppPersistsBoundsAndReflectsInWorkload asserts ScaleApp persists
// min/max and the rendered KEDA block reflects them.
func TestScaleAppPersistsBoundsAndReflectsInWorkload(t *testing.T) {
	svc, fb, _ := newReleaseSvc(t)
	ctx := context.Background()

	app, _ := svc.CreateApp(ctx, "org-1", CreateAppInput{Name: "web", Image: "nginx:1"})

	scaled, err := svc.ScaleApp(ctx, "org-1", app.ID, ScaleAppInput{MinReplicas: intptr(0), MaxReplicas: intptr(7)})
	if err != nil {
		t.Fatalf("scale app: %v", err)
	}
	if scaled.MinReplicas != 0 || scaled.MaxReplicas != 7 {
		t.Fatalf("scaled bounds = %d/%d, want 0/7", scaled.MinReplicas, scaled.MaxReplicas)
	}
	wl := fb.Applied["vortex-org-1-default/web"]
	if wl.Scaling.MinReplicas != 0 || wl.Scaling.MaxReplicas != 7 {
		t.Fatalf("workload scaling = %+v, want min 0 max 7", wl.Scaling)
	}
	// Incoherent bounds rejected.
	if _, err := svc.ScaleApp(ctx, "org-1", app.ID, ScaleAppInput{MinReplicas: intptr(5), MaxReplicas: intptr(2)}); !errors.Is(err, ErrInvalidScale) {
		t.Fatalf("incoherent scale err = %v, want ErrInvalidScale", err)
	}
}

// TestStatelessScaleToZeroDatabaseFloor asserts a stateless app's KEDA block can
// have minReplicaCount=0 while a database stays >=1.
func TestStatelessScaleToZeroDatabaseFloor(t *testing.T) {
	// Stateless app scaled to zero.
	app := kube.Scaling{MinReplicas: 0, MaxReplicas: 5, PollingInterval: 30, CooldownPeriod: 300, CPUUtilization: 70}
	stateless := kube.BuildKedaForTest(app, false)
	if stateless["minReplicaCount"].(int) != 0 {
		t.Fatalf("stateless minReplicaCount = %v, want 0", stateless["minReplicaCount"])
	}
	if stateless["idleReplicaCount"] != 0 {
		t.Fatalf("stateless idleReplicaCount = %v, want 0 (scale-to-zero)", stateless["idleReplicaCount"])
	}

	// Database with the same (0) request is floored to 1 and never scales to zero.
	db := kube.BuildKedaForTest(app, true)
	if db["minReplicaCount"].(int) != 1 {
		t.Fatalf("database minReplicaCount = %v, want 1 (floor)", db["minReplicaCount"])
	}
	if _, ok := db["idleReplicaCount"]; ok {
		t.Fatalf("database must not set idleReplicaCount (no scale-to-zero)")
	}
}

// TestAdminSettingsDriveKedaDefaults asserts the KEDA defaults come from platform
// settings (admin/DB-driven), not hardcoded constants.
func TestAdminSettingsDriveKedaDefaults(t *testing.T) {
	svc, fb, st := newReleaseSvc(t)
	ctx := context.Background()

	// Admin sets a platform-wide scale-to-zero default with a custom ceiling.
	set, _ := st.GetSettings(ctx)
	set.KedaDefaultMinReplicas = 0
	set.KedaDefaultMaxReplicas = 9
	set.KedaCPUUtilization = 55
	if err := st.UpdateSettings(ctx, set); err != nil {
		t.Fatalf("update settings: %v", err)
	}

	app, _ := svc.CreateApp(ctx, "org-1", CreateAppInput{Name: "web", Image: "nginx:1"})
	wl := fb.Applied["vortex-org-1-default/web"]
	if wl.Scaling.MinReplicas != 0 || wl.Scaling.MaxReplicas != 9 || wl.Scaling.CPUUtilization != 55 {
		t.Fatalf("workload scaling = %+v, want admin defaults (0/9, cpu 55)", wl.Scaling)
	}
	_ = app
}

func strptr(s string) *string   { return &s }
func intptr(i int) *int         { return &i }
func f64ptr(f float64) *float64 { return &f }
