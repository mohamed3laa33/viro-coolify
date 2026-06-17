package platform

import (
	"context"
	"errors"
	"testing"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/coolify"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/store"
)

// newSvc returns a platform service in demo mode (Coolify not configured, no network).
func newSvc() *Service {
	return NewService(store.NewMemoryStore(), coolify.NewClient("", ""))
}

func TestAppLifecycle(t *testing.T) {
	svc := newSvc()
	ctx := context.Background()

	app, err := svc.CreateApp(ctx, "org-1", CreateAppInput{Name: "web"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if app.OrgID != "org-1" || app.Status != "created" {
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
	svc := newSvc()
	ctx := context.Background()
	db, err := svc.CreateDatabase(ctx, "org-1", CreateDatabaseInput{Name: "main", Engine: "PostgreSQL"})
	if err != nil {
		t.Fatalf("create db: %v", err)
	}
	if db.Engine != "postgresql" {
		t.Fatalf("engine not normalized: %q", db.Engine)
	}
	dbs, _ := svc.ListDatabases(ctx, "org-1")
	if len(dbs) != 1 {
		t.Fatalf("expected 1 db, got %d", len(dbs))
	}
	other, _ := svc.ListDatabases(ctx, "org-2")
	if len(other) != 0 {
		t.Fatalf("expected 0 dbs for org-2, got %d", len(other))
	}
}
