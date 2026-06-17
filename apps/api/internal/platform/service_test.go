package platform

import (
	"context"
	"errors"
	"testing"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/billing"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/kube"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/store"
)

// newSvc returns a platform service backed by an in-memory store and a real
// in-memory kube test double (FakeBackend) — no network, no demo skip path.
func newSvc() *Service {
	st := store.NewMemoryStore()
	return NewService(st, kube.NewFakeBackend(), billing.NewService(st, nil))
}

// newSvcWithFake is like newSvc but also returns the FakeBackend so tests can
// assert what was applied to / acted on the deploy backend.
func newSvcWithFake() (*Service, *kube.FakeBackend) {
	st := store.NewMemoryStore()
	fb := kube.NewFakeBackend()
	return NewService(st, fb, billing.NewService(st, nil)), fb
}

func TestAppLifecycle(t *testing.T) {
	svc := newSvc()
	ctx := context.Background()

	app, err := svc.CreateApp(ctx, "org-1", CreateAppInput{Name: "web"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if app.OrgID != "org-1" || app.Status != "queued" {
		t.Fatalf("unexpected app: %+v", app)
	}

	apps, _ := svc.ListApps(ctx, "org-1")
	if len(apps) != 1 {
		t.Fatalf("expected 1 app, got %d", len(apps))
	}

	deployed, err := svc.Deploy(ctx, "org-1", app.ID)
	if err != nil || deployed.Status != "deploying" {
		t.Fatalf("deploy: %v status=%q", err, deployed.Status)
	}
	stopped, _ := svc.Stop(ctx, "org-1", app.ID)
	if stopped.Status != "stopped" {
		t.Fatalf("stop status = %q", stopped.Status)
	}

	if err := svc.Delete(ctx, "org-1", app.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := svc.GetApp(ctx, "org-1", app.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestTenantIsolation(t *testing.T) {
	svc := newSvc()
	ctx := context.Background()
	app, _ := svc.CreateApp(ctx, "org-1", CreateAppInput{Name: "web"})

	// Another org cannot see or act on org-1's app.
	if _, err := svc.GetApp(ctx, "org-2", app.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant get: expected ErrNotFound, got %v", err)
	}
	if _, err := svc.Deploy(ctx, "org-2", app.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant deploy: expected ErrNotFound, got %v", err)
	}
	apps, _ := svc.ListApps(ctx, "org-2")
	if len(apps) != 0 {
		t.Fatalf("expected org-2 to have 0 apps, got %d", len(apps))
	}
}

func TestDatabases(t *testing.T) {
	svc, fb := newSvcWithFake()
	ctx := context.Background()
	db, err := svc.CreateDatabase(ctx, "org-1", CreateDatabaseInput{Name: "main", Engine: "PostgreSQL"})
	if err != nil {
		t.Fatalf("create db: %v", err)
	}
	if db.Engine != "postgresql" {
		t.Fatalf("engine not normalized: %q", db.Engine)
	}
	// The database is actually deployed (not just a store record): placement is
	// recorded and the backend received an Apply for a stateful "database" workload.
	if db.Status != "deploying" || db.Release == "" || db.Namespace == "" {
		t.Fatalf("database not deployed: %+v", db)
	}
	w, ok := fb.Applied[db.Namespace+"/"+db.Release]
	if !ok {
		t.Fatalf("backend did not record the database apply")
	}
	if w.Kind != "database" || w.Image != "postgres:16-alpine" || w.Port != 5432 {
		t.Fatalf("unexpected database workload: %+v", w)
	}

	dbs, _ := svc.ListDatabases(ctx, "org-1")
	if len(dbs) != 1 {
		t.Fatalf("expected 1 db, got %d", len(dbs))
	}
	// Tenant separation: org-2 sees none of org-1's databases and cannot delete them.
	other, _ := svc.ListDatabases(ctx, "org-2")
	if len(other) != 0 {
		t.Fatalf("expected 0 dbs for org-2, got %d", len(other))
	}
	if err := svc.DeleteDatabase(ctx, "org-2", db.ID); err == nil {
		t.Fatal("cross-tenant database delete should fail")
	}

	// Owner can delete; the backend release is uninstalled and the record removed.
	if err := svc.DeleteDatabase(ctx, "org-1", db.ID); err != nil {
		t.Fatalf("delete db: %v", err)
	}
	if dbs, _ := svc.ListDatabases(ctx, "org-1"); len(dbs) != 0 {
		t.Fatalf("expected 0 dbs after delete, got %d", len(dbs))
	}
}

func TestCreateRedisDeploys(t *testing.T) {
	svc, fb := newSvcWithFake()
	db, err := svc.CreateDatabase(context.Background(), "org-r", CreateDatabaseInput{Name: "cache", Engine: "redis"})
	if err != nil {
		t.Fatalf("create redis: %v", err)
	}
	w, ok := fb.Applied[db.Namespace+"/"+db.Release]
	if !ok || w.Image != "redis:7-alpine" || w.Port != 6379 || w.Kind != "database" {
		t.Fatalf("redis not deployed as expected: %+v (ok=%v)", w, ok)
	}
}

func TestCreateAppFromImageDeploys(t *testing.T) {
	svc, fb := newSvcWithFake()
	app, err := svc.CreateApp(context.Background(), "org-img", CreateAppInput{Name: "api", Image: "nginx:1.27"})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	if app.Status != "deploying" || app.Release == "" {
		t.Fatalf("image app not deployed: %+v", app)
	}
	w, ok := fb.Applied[app.Namespace+"/"+app.Release]
	if !ok || w.Kind != "app" || w.Image != "nginx:1.27" {
		t.Fatalf("unexpected app workload: %+v (ok=%v)", w, ok)
	}
}

func TestCreateAppGitOnlyStaysQueued(t *testing.T) {
	svc, fb := newSvcWithFake()
	app, err := svc.CreateApp(context.Background(), "org-git", CreateAppInput{Name: "web", GitRepository: "https://example.com/repo.git"})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	if app.Status != "queued" || app.Release != "" {
		t.Fatalf("git-only app should stay queued until built: %+v", app)
	}
	if len(fb.Applied) != 0 {
		t.Fatalf("git-only app must not deploy yet, got %d applies", len(fb.Applied))
	}
}
