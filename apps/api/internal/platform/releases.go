package platform

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/kube"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/store"
)

// ErrNoRelease is returned when a rollback is requested but the app has no prior
// release to roll back to (or the requested revision does not exist).
var ErrNoRelease = errors.New("platform: no release to roll back to")

// configHash fingerprints the rendered spec (image + resources + env + domains) of
// a workload so two identical deploys hash equal and any change is detectable. It
// is deterministic: env keys and domains are sorted before hashing.
func configHash(w kube.Workload) string {
	var b strings.Builder
	fmt.Fprintf(&b, "image=%s\n", w.Image)
	fmt.Fprintf(&b, "cpu=%g\nmem=%d\n", w.CPU, w.MemoryMB)
	keys := make([]string, 0, len(w.Env))
	for k := range w.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "env:%s=%s\n", k, w.Env[k])
	}
	doms := append([]string(nil), w.Domains...)
	sort.Strings(doms)
	for _, d := range doms {
		fmt.Fprintf(&b, "dom:%s\n", d)
	}
	fmt.Fprintf(&b, "secret=%s\n", w.EnvSecretName)
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// recordRelease records a new ACTIVE release revision for an app after a successful
// Apply: it marks the prior active release superseded, computes the next monotonic
// revision, and inserts the new release. The new release captures the EXACT image +
// resources that were Applied (from the rendered workload) plus a config hash, so it
// is a faithful, re-deployable snapshot the app can be rolled back to.
//
// note is an optional human-readable annotation (e.g. "rollback to r2"); it is empty
// for ordinary deploys. supersededStatus controls what the prior active release is
// transitioned to: ReleaseSuperseded for an ordinary deploy, ReleaseRolledBack when
// a rollback steps away from it. A release-recording failure is returned so the
// caller can log it, but it must NEVER fail the user-visible deploy (the workload is
// already Applied) — callers therefore log-and-continue.
func (s *Service) recordRelease(ctx context.Context, app *domain.App, w kube.Workload, note string, supersededStatus domain.ReleaseStatus) (*domain.Release, error) {
	// The whole (compute-next-revision + supersede-prior-active + insert-new)
	// sequence must be atomic, or two concurrent deploys race: both read the same
	// MAX(revision), both try to insert it, and the unique (app_id, revision)
	// constraint rejects the loser — which would otherwise drop its release row
	// entirely (lost history / no rollback target). We run the sequence inside one
	// transaction and, on a unique-violation (store.ErrConflict) from a racing
	// writer that committed a higher revision first, RETRY the whole tx (re-reading
	// MAX) a few times so the loser still gets a fresh, higher revision.
	const maxAttempts = 5
	var rel *domain.Release
	for attempt := 0; attempt < maxAttempts; attempt++ {
		var out *domain.Release
		txErr := s.store.WithTx(ctx, func(tx store.Store) error {
			r, err := s.allocateAndInsertRelease(ctx, tx, app, w, note, supersededStatus)
			if err != nil {
				return err
			}
			out = r
			return nil
		})
		if txErr == nil {
			rel = out
			break
		}
		// A conflicting revision means another deploy committed first; re-read MAX
		// and try again. Any other error is fatal.
		if errors.Is(txErr, store.ErrConflict) {
			continue
		}
		return nil, txErr
	}
	if rel == nil {
		return nil, fmt.Errorf("platform: record release for app %s: %w after %d attempts", app.ID, store.ErrConflict, maxAttempts)
	}
	return rel, nil
}

// allocateAndInsertRelease computes the next monotonic revision (MAX+1),
// supersedes the prior active/deploying release, and inserts the new active
// release — all against the transaction-scoped store tx so the sequence is
// atomic. A duplicate revision surfaces as store.ErrConflict for the caller's
// retry loop.
func (s *Service) allocateAndInsertRelease(ctx context.Context, tx store.Store, app *domain.App, w kube.Workload, note string, supersededStatus domain.ReleaseStatus) (*domain.Release, error) {
	existing, err := tx.ListReleasesByApp(ctx, app.ID)
	if err != nil {
		return nil, err
	}
	nextRev := 1
	for i := range existing {
		r := existing[i]
		if r.Revision >= nextRev {
			nextRev = r.Revision + 1
		}
		if r.Status == domain.ReleaseActive || r.Status == domain.ReleaseDeploying {
			r.Status = supersededStatus
			if uerr := tx.UpdateRelease(ctx, &r); uerr != nil {
				return nil, uerr
			}
		}
	}
	rel := &domain.Release{
		ID:         s.idgen(),
		AppID:      app.ID,
		OrgID:      app.OrgID,
		Revision:   nextRev,
		Image:      w.Image,
		GitRef:     app.GitBranch,
		ConfigHash: configHash(w),
		CPU:        w.CPU,
		MemoryMB:   w.MemoryMB,
		Status:     domain.ReleaseActive,
		Note:       note,
		CreatedAt:  s.now(),
	}
	if err := tx.CreateRelease(ctx, rel); err != nil {
		return nil, err
	}
	return rel, nil
}

// ListReleases returns the org app's release history, newest revision first.
func (s *Service) ListReleases(ctx context.Context, orgID, appID string) ([]domain.Release, error) {
	if _, err := s.ownedApp(ctx, orgID, appID); err != nil {
		return nil, err
	}
	return s.store.ListReleasesByApp(ctx, appID)
}

// CurrentRelease returns the app's currently-active release (the highest-revision
// release whose status is active/deploying), or nil when the app has none yet.
func (s *Service) CurrentRelease(ctx context.Context, appID string) (*domain.Release, error) {
	rels, err := s.store.ListReleasesByApp(ctx, appID)
	if err != nil {
		return nil, err
	}
	for i := range rels {
		if rels[i].Status == domain.ReleaseActive || rels[i].Status == domain.ReleaseDeploying {
			r := rels[i]
			return &r, nil
		}
	}
	return nil, nil
}

// ReconcileReleaseStatus transitions the app's CURRENT release to mirror the
// reconciled MACHINE status the reconciler just observed and wrote to the app row:
//   - machineStatus "running" => the current release becomes active (it came up).
//   - machineStatus "failed"  => the current release becomes failed (it crashlooped).
//
// Any other machine status (pending/scaled-to-zero/etc.) leaves the release alone,
// and a superseded/rolled_back/failed release is never reanimated (only the current
// active/deploying release transitions). It is best-effort and idempotent: a no-op
// when the release is already in the target state. The reconciler calls this right
// after persisting the app's machine status so the release history reflects whether
// a deploy actually succeeded (the bug was every release stayed "active" forever).
func (s *Service) ReconcileReleaseStatus(ctx context.Context, appID, machineStatus string) error {
	var target domain.ReleaseStatus
	switch machineStatus {
	case "running":
		target = domain.ReleaseActive
	case "failed":
		target = domain.ReleaseFailed
	default:
		return nil // not a terminal machine phase that maps to a release outcome
	}
	cur, err := s.CurrentRelease(ctx, appID)
	if err != nil {
		return err
	}
	if cur == nil || cur.Status == target {
		return nil
	}
	cur.Status = target
	return s.store.UpdateRelease(ctx, cur)
}

// RollbackApp rolls an app back to a prior release revision. When revision is 0 it
// targets the most recent NON-active prior revision (the "previous" release).
//
// Semantics (explicit, by design): the rollback restores the TARGET release's IMAGE
// and RESOURCES (cpu/memory) — the spec that release was deployed with — while
// keeping the app's CURRENT env and CURRENT verified domains. Rolling back a bad
// image/size therefore does NOT silently discard config or domain changes made
// since; only the image+resources travel back in time. The app row's
// Image/CPU/MemoryMB are updated to the target's so a later plain Deploy keeps the
// rolled-back spec, then the workload is re-rendered and Applied. A NEW release row
// is recorded (active, Note="rollback to rN"); the previously-active release is
// marked rolled_back.
func (s *Service) RollbackApp(ctx context.Context, orgID, appID string, revision int) (*domain.App, error) {
	app, err := s.ownedApp(ctx, orgID, appID)
	if err != nil {
		return nil, err
	}
	// A rollback is a (re)deploy of paid work: re-gate subscription/spend-cap.
	if err := s.ensureActive(ctx, orgID); err != nil {
		return nil, err
	}
	rels, err := s.store.ListReleasesByApp(ctx, appID) // newest revision first
	if err != nil {
		return nil, err
	}
	target := pickRollbackTarget(rels, revision)
	if target == nil {
		return nil, ErrNoRelease
	}
	if strings.TrimSpace(target.Image) == "" {
		return nil, ErrNoImage
	}
	// Re-validate the rolled-back size against the org's CURRENT plan so a rollback
	// can't bring an oversized (e.g. pre-downgrade) workload back online.
	if err := s.checkWorkloadSize(ctx, orgID, target.CPU, target.MemoryMB); err != nil {
		return nil, err
	}

	// Restore the target's image + resources onto the app; keep current env/domains.
	app.Image = target.Image
	app.CPU = target.CPU
	app.MemoryMB = target.MemoryMB

	// A user who stopped the app must not be resurrected by a rollback: persist the
	// rolled-back spec/bounds so a later Start/Deploy ships them, but SKIP the
	// re-Apply (and the status flip) — mirroring the build-path stopped guard.
	if app.Status == "stopped" {
		if err := s.persistAppSpec(ctx, app); err != nil {
			return nil, err
		}
		return app, nil
	}

	orgSlug := s.orgSlug(ctx, orgID)
	projSlug := s.projectSlug(ctx, app.ProjectID)
	release, host, err := s.applyApp(ctx, app, orgSlug, projSlug)
	if err != nil {
		return nil, err
	}
	app.Release = release
	app.Host = host
	app.Status = "deploying"
	// Re-fetch immediately before the post-Apply write and persist ONLY the
	// operation-owned fields, so a concurrent async build/edit (env/domains) that
	// landed during Apply is not clobbered. Mirrors the build path discipline.
	if err := s.persistAppSpec(ctx, app); err != nil {
		return nil, err
	}

	// Record the new (rollback) release. The previously-active release is marked
	// rolled_back rather than superseded so the history reads honestly.
	wl, werr := s.appWorkload(ctx, app, orgSlug, projSlug)
	if werr != nil {
		// The deploy already succeeded; we just can't snapshot it. Log-and-continue.
		s.logRelease(appID, werr)
		return app, nil
	}
	note := fmt.Sprintf("rollback to r%d", target.Revision)
	if _, rerr := s.recordRelease(ctx, app, wl, note, domain.ReleaseRolledBack); rerr != nil {
		s.logRelease(appID, rerr)
	}
	return app, nil
}

// persistAppSpec re-fetches the app row immediately before writing and persists
// ONLY the operation-owned fields (Image/CPU/MemoryMB/MinReplicas/MaxReplicas/
// Release/Host/Status), copying them from app onto the fresh row. This mirrors the
// async build path's concurrency discipline: a plain UpdateApp(app) would re-write
// the WHOLE row and silently clobber a concurrent edit/build that changed
// env/domains/git source between our read and this write. On success app is updated
// in place to reflect the persisted row so the caller returns a consistent view.
func (s *Service) persistAppSpec(ctx context.Context, app *domain.App) error {
	fresh, err := s.store.GetApp(ctx, app.ID)
	if err != nil {
		return err
	}
	fresh.Image = app.Image
	fresh.CPU = app.CPU
	fresh.MemoryMB = app.MemoryMB
	fresh.MinReplicas = app.MinReplicas
	fresh.MaxReplicas = app.MaxReplicas
	fresh.Release = app.Release
	fresh.Host = app.Host
	fresh.Status = app.Status
	if err := s.store.UpdateApp(ctx, fresh); err != nil {
		return err
	}
	*app = *fresh
	return nil
}

// pickRollbackTarget selects the release to roll back to from the app's releases
// (which arrive newest-revision-first). When revision>0 it matches that exact
// revision. When revision==0 it picks the most recent release that is NOT the
// current active/deploying one — i.e. the previous release.
func pickRollbackTarget(rels []domain.Release, revision int) *domain.Release {
	if revision > 0 {
		for i := range rels {
			if rels[i].Revision == revision {
				return &rels[i]
			}
		}
		return nil
	}
	// Default: the previous (most recent non-active) release.
	for i := range rels {
		if rels[i].Status != domain.ReleaseActive && rels[i].Status != domain.ReleaseDeploying {
			return &rels[i]
		}
	}
	return nil
}

// logRelease is a best-effort log for a release-bookkeeping failure that must not
// surface to the user (the deploy itself already succeeded).
func (s *Service) logRelease(appID string, err error) {
	log.Printf("platform: record release for app %s failed: %v", appID, err)
}

// UpdateAppInput carries the mutable app spec fields a PATCH may change. A nil
// pointer means "leave unchanged"; a non-nil pointer (even to a zero value) sets it.
type UpdateAppInput struct {
	Image         *string
	CPU           *float64
	MemoryMB      *int
	GitRepository *string
	GitBranch     *string
}

// UpdateApp updates an app's spec (image / resources / git source) and, when the
// app is already deployed, re-Applies it (recording a new release). It re-validates
// the new size against the org's plan quota and re-checks the subscription/spend-cap
// (EnsureActive), so an update is gated exactly like a deploy. Unset fields are left
// unchanged.
func (s *Service) UpdateApp(ctx context.Context, orgID, appID string, in UpdateAppInput) (*domain.App, error) {
	app, err := s.ownedApp(ctx, orgID, appID)
	if err != nil {
		return nil, err
	}
	if err := s.ensureActive(ctx, orgID); err != nil {
		return nil, err
	}

	if in.Image != nil {
		app.Image = strings.TrimSpace(*in.Image)
	}
	if in.GitRepository != nil {
		app.GitRepository = strings.TrimSpace(*in.GitRepository)
	}
	if in.GitBranch != nil {
		b := strings.TrimSpace(*in.GitBranch)
		if b == "" {
			b = "main"
		}
		app.GitBranch = b
	}
	cpu, memMB := app.CPU, app.MemoryMB
	if in.CPU != nil {
		cpu = *in.CPU
	}
	if in.MemoryMB != nil {
		memMB = *in.MemoryMB
	}
	cpu, memMB = s.normalizeResources(ctx, cpu, memMB)
	// Re-validate the (possibly) new size against the org's current plan ceiling.
	if err := s.checkWorkloadSize(ctx, orgID, cpu, memMB); err != nil {
		return nil, err
	}
	app.CPU = cpu
	app.MemoryMB = memMB

	// Persist the spec change first so it survives even if the (optional) re-Apply
	// below has nothing to do (app not yet deployed). persistAppSpec re-fetches and
	// writes only the operation-owned fields so a concurrent build/edit is preserved.
	if err := s.persistAppSpec(ctx, app); err != nil {
		return nil, err
	}

	// A user who stopped the app must not be resurrected by an update: the spec is
	// persisted above, but we SKIP the re-Apply and the "deploying" flip (mirrors the
	// build-path stopped guard and avoids fighting the KEDA pause).
	if app.Status == "stopped" {
		return app, nil
	}

	// Re-Apply only when the app is already deployed (has a Release) and has an image
	// to ship. A git app with no image yet, or a never-deployed app, just keeps the
	// stored spec until its next deploy/build.
	if app.Release == "" || strings.TrimSpace(app.Image) == "" {
		return app, nil
	}
	orgSlug := s.orgSlug(ctx, orgID)
	projSlug := s.projectSlug(ctx, app.ProjectID)
	release, host, err := s.applyApp(ctx, app, orgSlug, projSlug)
	if err != nil {
		return nil, err
	}
	app.Release = release
	app.Host = host
	app.Status = "deploying"
	// Re-fetch before the post-Apply write so a concurrent build/edit is preserved.
	if err := s.persistAppSpec(ctx, app); err != nil {
		return nil, err
	}
	if wl, werr := s.appWorkload(ctx, app, orgSlug, projSlug); werr == nil {
		if _, rerr := s.recordRelease(ctx, app, wl, "", domain.ReleaseSuperseded); rerr != nil {
			s.logRelease(appID, rerr)
		}
	} else {
		s.logRelease(appID, werr)
	}
	return app, nil
}

// ScaleAppInput carries the autoscaling-bound changes a scale request may make. A
// nil pointer means "leave unchanged".
type ScaleAppInput struct {
	MinReplicas *int
	MaxReplicas *int
}

// ScaleApp sets an app's autoscaling bounds (persisted on the app) and, when the app
// is deployed, re-Applies it so KEDA picks up the new min/max. A stateless app may
// be scaled to MinReplicas=0 (scale-to-zero). The bounds are validated for coherence
// (min<=max, non-negative). Re-applying records a new release so the change is in the
// app's history.
func (s *Service) ScaleApp(ctx context.Context, orgID, appID string, in ScaleAppInput) (*domain.App, error) {
	app, err := s.ownedApp(ctx, orgID, appID)
	if err != nil {
		return nil, err
	}
	minR, maxR := app.MinReplicas, app.MaxReplicas
	if in.MinReplicas != nil {
		minR = *in.MinReplicas
	}
	if in.MaxReplicas != nil {
		maxR = *in.MaxReplicas
	}
	if minR < 0 {
		minR = 0
	}
	if maxR < 0 {
		maxR = 0
	}
	if maxR > 0 && minR > maxR {
		return nil, fmt.Errorf("%w: minReplicas %d exceeds maxReplicas %d", ErrInvalidScale, minR, maxR)
	}
	// Enforce the admin/DB-driven autoscaling ceiling so a tenant can't set an
	// unbounded MaxReplicas and bypass the plan/platform limit. The ceiling is read
	// live from platform settings (KedaMaxReplicasCeiling, admin-tunable).
	if ceil := s.kedaMaxReplicasCeiling(ctx); ceil > 0 && maxR > ceil {
		return nil, fmt.Errorf("%w: maxReplicas %d exceeds platform ceiling %d", ErrInvalidScale, maxR, ceil)
	}
	app.MinReplicas = minR
	app.MaxReplicas = maxR
	if err := s.persistAppSpec(ctx, app); err != nil {
		return nil, err
	}

	// A user who stopped the app must not be resurrected by a scale: the bounds are
	// persisted above, but we SKIP the re-Apply so we don't fight the KEDA pause.
	if app.Status == "stopped" {
		return app, nil
	}

	// Re-Apply so KEDA reflects the new bounds (only when deployed with an image).
	if app.Release == "" || strings.TrimSpace(app.Image) == "" {
		return app, nil
	}
	if err := s.ensureActive(ctx, orgID); err != nil {
		return nil, err
	}
	orgSlug := s.orgSlug(ctx, orgID)
	projSlug := s.projectSlug(ctx, app.ProjectID)
	release, host, err := s.applyApp(ctx, app, orgSlug, projSlug)
	if err != nil {
		return nil, err
	}
	app.Release = release
	app.Host = host
	// Re-fetch before the post-Apply write so a concurrent build/edit is preserved.
	if err := s.persistAppSpec(ctx, app); err != nil {
		return nil, err
	}
	if wl, werr := s.appWorkload(ctx, app, orgSlug, projSlug); werr == nil {
		if _, rerr := s.recordRelease(ctx, app, wl, "scale", domain.ReleaseSuperseded); rerr != nil {
			s.logRelease(appID, rerr)
		}
	} else {
		s.logRelease(appID, werr)
	}
	return app, nil
}

// kedaMaxReplicasCeiling returns the admin/DB-driven autoscaling ceiling
// (KedaMaxReplicasCeiling) from platform settings, or 0 when settings are
// unavailable / the ceiling is unset (0 disables the ceiling check).
func (s *Service) kedaMaxReplicasCeiling(ctx context.Context) int {
	if set, err := s.store.GetSettings(ctx); err == nil && set != nil {
		return set.KedaMaxReplicasCeiling
	}
	return 0
}

// ErrInvalidScale is returned when scale bounds are incoherent (min>max) or
// exceed the admin/DB-driven autoscaling ceiling.
var ErrInvalidScale = errors.New("platform: invalid scale bounds")
