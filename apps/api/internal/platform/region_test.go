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

// The MemoryStore seeds DefaultRegion "fra1" and Regions [fra1,nyc1,sfo3,sgp1].

func TestCreateApp_DefaultsAndPersistsRegion(t *testing.T) {
	svc, fb := newSvcWithFake()
	ctx := context.Background()

	app, err := svc.CreateApp(ctx, "org-1", CreateAppInput{Name: "web", Image: "nginx:1"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// No region requested => platform default.
	if app.Region != "fra1" {
		t.Fatalf("want default region fra1, got %q", app.Region)
	}
	// Persisted on the stored app.
	got, err := svc.GetApp(ctx, "org-1", app.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Region != "fra1" {
		t.Fatalf("persisted region = %q, want fra1", got.Region)
	}
	// Plumbed onto the workload Apply'd to the backend.
	var found bool
	for _, w := range fb.Applied {
		if w.Name == "web" {
			found = true
			if w.Region != "fra1" {
				t.Fatalf("workload region = %q, want fra1", w.Region)
			}
		}
	}
	if !found {
		t.Fatalf("no Apply recorded for app web")
	}
}

func TestCreateApp_ExplicitValidRegion(t *testing.T) {
	svc := newSvc()
	app, err := svc.CreateApp(context.Background(), "org-1", CreateAppInput{Name: "web", Image: "nginx:1", Region: "nyc1"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if app.Region != "nyc1" {
		t.Fatalf("want region nyc1, got %q", app.Region)
	}
}

func TestCreateApp_InvalidRegionRejected(t *testing.T) {
	svc := newSvc()
	_, err := svc.CreateApp(context.Background(), "org-1", CreateAppInput{Name: "web", Image: "nginx:1", Region: "mars1"})
	if !errors.Is(err, ErrInvalidRegion) {
		t.Fatalf("want ErrInvalidRegion, got %v", err)
	}
}

func TestCreateDatabase_InvalidRegionRejected(t *testing.T) {
	svc := newSvc()
	_, err := svc.CreateDatabase(context.Background(), "org-1", CreateDatabaseInput{Name: "db", Engine: "postgresql", Region: "nowhere"})
	if !errors.Is(err, ErrInvalidRegion) {
		t.Fatalf("want ErrInvalidRegion, got %v", err)
	}
}

func TestCreateDatabase_DefaultsRegion(t *testing.T) {
	svc, fb := newSvcWithFake()
	ctx := context.Background()
	db, err := svc.CreateDatabase(ctx, "org-1", CreateDatabaseInput{Name: "db", Engine: "postgresql"})
	if err != nil {
		t.Fatalf("create database: %v", err)
	}
	if db.Region != "fra1" {
		t.Fatalf("want default region fra1, got %q", db.Region)
	}
	for _, w := range fb.Applied {
		if w.Kind == "database" && w.Region != "fra1" {
			t.Fatalf("database workload region = %q, want fra1", w.Region)
		}
	}
}

// TestResolveRegion_NoRegionsConfigured asserts the seam is dormant (passes the
// request through, no validation) when platform settings carry no region list.
func TestResolveRegion_NoRegionsConfigured(t *testing.T) {
	st := store.NewMemoryStore()
	// Clear the seeded regions so validation is skipped.
	if err := st.UpdateSettings(context.Background(), &domain.PlatformSettings{
		DefaultCPU:      0.25,
		DefaultMemoryMB: 256,
		Regions:         []string{},
	}); err != nil {
		t.Fatalf("update settings: %v", err)
	}
	svc := NewService(st, kube.NewFakeBackend(), billing.NewService(st, nil))
	got, err := svc.resolveRegion(context.Background(), "anything")
	if err != nil {
		t.Fatalf("want no error when no regions configured, got %v", err)
	}
	if got != "anything" {
		t.Fatalf("want pass-through region, got %q", got)
	}
}
