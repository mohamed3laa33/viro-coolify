package platform

import (
	"context"
	"errors"
	"testing"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/kube"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/store"
)

// TestRecordReleaseSequentialRevisionsAndConflict asserts that two sequential
// recordRelease calls produce rev1 then rev2 (with the prior superseded), and that
// inserting a DUPLICATE (app_id, revision) is rejected by the store with
// ErrConflict — so the two stores agree on revision uniqueness (fix #1).
func TestRecordReleaseSequentialRevisionsAndConflict(t *testing.T) {
	svc, _, st := newReleaseSvc(t)
	ctx := context.Background()

	app := &domain.App{ID: "app-rr", OrgID: "org-1", Name: "web", Image: "nginx:1"}
	if err := st.CreateApp(ctx, app); err != nil {
		t.Fatalf("create app: %v", err)
	}
	w := kube.Workload{Name: "web", Image: "nginx:1", CPU: 0.25, MemoryMB: 128}

	r1, err := svc.recordRelease(ctx, app, w, "", domain.ReleaseSuperseded)
	if err != nil {
		t.Fatalf("recordRelease #1: %v", err)
	}
	if r1.Revision != 1 || r1.Status != domain.ReleaseActive {
		t.Fatalf("rev1 = %+v, want revision 1 active", r1)
	}

	w.Image = "nginx:2"
	r2, err := svc.recordRelease(ctx, app, w, "", domain.ReleaseSuperseded)
	if err != nil {
		t.Fatalf("recordRelease #2: %v", err)
	}
	if r2.Revision != 2 || r2.Status != domain.ReleaseActive {
		t.Fatalf("rev2 = %+v, want revision 2 active", r2)
	}

	// rev1 must now be superseded; exactly one active release remains.
	rels, err := st.ListReleasesByApp(ctx, app.ID)
	if err != nil {
		t.Fatalf("list releases: %v", err)
	}
	if len(rels) != 2 {
		t.Fatalf("releases = %d, want 2", len(rels))
	}
	active := 0
	var sawSuperseded bool
	for _, r := range rels {
		if r.Status == domain.ReleaseActive {
			active++
		}
		if r.Revision == 1 && r.Status == domain.ReleaseSuperseded {
			sawSuperseded = true
		}
	}
	if active != 1 {
		t.Fatalf("active releases = %d, want 1", active)
	}
	if !sawSuperseded {
		t.Fatalf("rev1 should be superseded after rev2: %+v", rels)
	}

	// A forced DUPLICATE revision for the same app is rejected with ErrConflict
	// (mirrors Postgres UNIQUE(app_id, revision)).
	dup := &domain.Release{
		ID: "dup-rel", AppID: app.ID, OrgID: app.OrgID, Revision: 2,
		Image: "nginx:dup", Status: domain.ReleaseActive, CreatedAt: svc.now(),
	}
	if err := st.CreateRelease(ctx, dup); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate revision err = %v, want store.ErrConflict", err)
	}
}

// TestScaleStoppedAppDoesNotApply asserts scaling a STOPPED app persists the new
// bounds but does NOT re-Apply (no resurrection) and leaves it stopped (fix #3).
func TestScaleStoppedAppDoesNotApply(t *testing.T) {
	svc, fb, _ := newReleaseSvc(t)
	ctx := context.Background()

	app, err := svc.CreateApp(ctx, "org-1", CreateAppInput{Name: "web", Image: "nginx:1"})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	if _, err := svc.Stop(ctx, "org-1", app.ID); err != nil {
		t.Fatalf("stop: %v", err)
	}

	// Snapshot the applied-workload generation so we can detect a re-Apply.
	ns := "vortex-org-1-default"
	before := fb.Applied[ns+"/web"]

	scaled, err := svc.ScaleApp(ctx, "org-1", app.ID, ScaleAppInput{MinReplicas: intptr(0), MaxReplicas: intptr(3)})
	if err != nil {
		t.Fatalf("scale stopped app: %v", err)
	}
	if scaled.Status != "stopped" {
		t.Fatalf("status after scaling stopped app = %q, want stopped", scaled.Status)
	}
	if scaled.MinReplicas != 0 || scaled.MaxReplicas != 3 {
		t.Fatalf("bounds not persisted: %d/%d, want 0/3", scaled.MinReplicas, scaled.MaxReplicas)
	}
	// The workload must NOT have been re-Applied with the new bounds (still the
	// pre-stop generation: a re-Apply would have rendered min 0 / max 3).
	after := fb.Applied[ns+"/web"]
	if after.Scaling.MaxReplicas == 3 && before.Scaling.MaxReplicas != 3 {
		t.Fatalf("stopped app was re-Applied (resurrected): %+v", after.Scaling)
	}
}

// TestUpdateStoppedAppDoesNotApply asserts updating a STOPPED app persists the new
// spec but does NOT re-Apply / flip to deploying (fix #3).
func TestUpdateStoppedAppDoesNotApply(t *testing.T) {
	svc, fb, _ := newReleaseSvc(t)
	ctx := context.Background()

	app, err := svc.CreateApp(ctx, "org-1", CreateAppInput{Name: "web", Image: "nginx:1"})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	if _, err := svc.Stop(ctx, "org-1", app.ID); err != nil {
		t.Fatalf("stop: %v", err)
	}

	updated, err := svc.UpdateApp(ctx, "org-1", app.ID, UpdateAppInput{Image: strptr("nginx:9")})
	if err != nil {
		t.Fatalf("update stopped app: %v", err)
	}
	if updated.Status != "stopped" {
		t.Fatalf("status after updating stopped app = %q, want stopped (not deploying)", updated.Status)
	}
	if updated.Image != "nginx:9" {
		t.Fatalf("image not persisted on stopped app: %q", updated.Image)
	}
	// The backend must not have received the new image (no re-Apply).
	if wl := fb.Applied["vortex-org-1-default/web"]; wl.Image == "nginx:9" {
		t.Fatalf("stopped app was re-Applied with the new image (resurrected)")
	}
}

// TestScaleCeilingEnforced asserts ScaleApp rejects a MaxReplicas above the
// admin/DB-driven KedaMaxReplicasCeiling and accepts one at/under it (fix #4).
func TestScaleCeilingEnforced(t *testing.T) {
	svc, _, st := newReleaseSvc(t)
	ctx := context.Background()

	// Admin sets a low ceiling.
	set, _ := st.GetSettings(ctx)
	set.KedaMaxReplicasCeiling = 4
	if err := st.UpdateSettings(ctx, set); err != nil {
		t.Fatalf("update settings: %v", err)
	}

	app, err := svc.CreateApp(ctx, "org-1", CreateAppInput{Name: "web", Image: "nginx:1"})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}

	// Above the ceiling is rejected.
	if _, err := svc.ScaleApp(ctx, "org-1", app.ID, ScaleAppInput{MaxReplicas: intptr(5)}); !errors.Is(err, ErrInvalidScale) {
		t.Fatalf("over-ceiling scale err = %v, want ErrInvalidScale", err)
	}
	// At the ceiling is accepted.
	if _, err := svc.ScaleApp(ctx, "org-1", app.ID, ScaleAppInput{MaxReplicas: intptr(4)}); err != nil {
		t.Fatalf("at-ceiling scale should be accepted: %v", err)
	}
}
