package platform

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/billing"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/kube"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/store"
)

// newSvcWithStore builds a platform service over a caller-supplied store so the
// test can seed subscriptions / spend caps that gating consults.
func newSvcWithStore(st store.Store) *Service {
	return NewService(st, kube.NewFakeBackend(), billing.NewService(st, nil))
}

func seedSub(t *testing.T, st store.Store, orgID, planID string, status domain.SubscriptionStatus) {
	t.Helper()
	if err := st.UpsertSubscription(context.Background(), &domain.Subscription{
		OrgID: orgID, PlanID: planID, Status: status,
		CurrentPeriodEnd: time.Now().AddDate(0, 1, 0),
	}); err != nil {
		t.Fatalf("seed sub: %v", err)
	}
}

func TestCreateAppGatedBySubscription(t *testing.T) {
	for _, status := range []domain.SubscriptionStatus{domain.SubCanceled, domain.SubUnpaid, domain.SubPastDue} {
		st := store.NewMemoryStore()
		svc := newSvcWithStore(st)
		ctx := context.Background()
		seedSub(t, st, "org-1", "launch", status)

		_, err := svc.CreateApp(ctx, "org-1", CreateAppInput{Name: "web", Image: "nginx"})
		if !errors.Is(err, ErrPaymentRequired) {
			t.Fatalf("%s: expected ErrPaymentRequired, got %v", status, err)
		}
	}
}

func TestCreateAppAllowedWhenActive(t *testing.T) {
	st := store.NewMemoryStore()
	svc := newSvcWithStore(st)
	ctx := context.Background()
	seedSub(t, st, "org-1", "launch", domain.SubActive)

	if _, err := svc.CreateApp(ctx, "org-1", CreateAppInput{Name: "web", Image: "nginx"}); err != nil {
		t.Fatalf("active org should provision, got %v", err)
	}
}

func TestCreateServiceAndDatabaseGated(t *testing.T) {
	st := store.NewMemoryStore()
	svc := newSvcWithStore(st)
	ctx := context.Background()
	seedSub(t, st, "org-1", "launch", domain.SubCanceled)

	if _, err := svc.CreateService(ctx, "org-1", "", CreateServiceInput{TemplateKey: "wordpress", Name: "wp"}); !errors.Is(err, ErrPaymentRequired) {
		t.Fatalf("CreateService canceled: expected 402, got %v", err)
	}
	if _, err := svc.CreateDatabase(ctx, "org-1", CreateDatabaseInput{Name: "db", Engine: "postgresql"}); !errors.Is(err, ErrPaymentRequired) {
		t.Fatalf("CreateDatabase canceled: expected 402, got %v", err)
	}
}

func TestDeployGatedWhenCanceled(t *testing.T) {
	st := store.NewMemoryStore()
	svc := newSvcWithStore(st)
	ctx := context.Background()
	seedSub(t, st, "org-1", "launch", domain.SubActive)

	app, err := svc.CreateApp(ctx, "org-1", CreateAppInput{Name: "web", Image: "nginx"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Subscription is canceled after creation: a redeploy must be blocked.
	seedSub(t, st, "org-1", "launch", domain.SubCanceled)
	if _, err := svc.Deploy(ctx, "org-1", app.ID); !errors.Is(err, ErrPaymentRequired) {
		t.Fatalf("Deploy after cancel: expected 402, got %v", err)
	}
}

func TestDeployRuntimeQuotaRecheckOnDowngrade(t *testing.T) {
	st := store.NewMemoryStore()
	svc := newSvcWithStore(st)
	ctx := context.Background()
	// Create under a generous plan (scale: MaxCPU 2), sized at 2 vCPU.
	seedSub(t, st, "org-1", "scale", domain.SubActive)
	app, err := svc.CreateApp(ctx, "org-1", CreateAppInput{Name: "big", Image: "nginx", CPU: 2, MemoryMB: 4096})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Downgrade to hobby (MaxCPU 0.5): the oversized workload can't be redeployed.
	seedSub(t, st, "org-1", "hobby", domain.SubActive)
	if _, err := svc.Deploy(ctx, "org-1", app.ID); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("Deploy after downgrade: expected ErrQuotaExceeded, got %v", err)
	}
	// Restart is gated the same way.
	if _, err := svc.Restart(ctx, "org-1", app.ID); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("Restart after downgrade: expected ErrQuotaExceeded, got %v", err)
	}
}

func TestCreateAppBlockedBySpendCap(t *testing.T) {
	st := store.NewMemoryStore()
	svc := newSvcWithStore(st)
	ctx := context.Background()
	if err := st.CreateOrganization(ctx, &domain.Organization{ID: "org-1", Slug: "org-1", SpendCapCents: 500}); err != nil {
		t.Fatalf("org: %v", err)
	}
	seedSub(t, st, "org-1", "hobby", domain.SubActive)
	// Metered usage so far (600 cents) exceeds the 500c cap.
	now := time.Now().UTC().Truncate(time.Hour)
	for i := 0; i < 600; i++ {
		_ = st.AddUsage(ctx, &domain.UsageRecord{
			ID:    "meter-org-1-" + now.Add(time.Duration(i)*time.Hour).Format("20060102T15"),
			OrgID: "org-1", Metric: billing.MeterMetric, Quantity: 1000, At: now.Add(time.Duration(i) * time.Hour),
		})
	}
	if _, err := svc.CreateApp(ctx, "org-1", CreateAppInput{Name: "web", Image: "nginx"}); !errors.Is(err, ErrPaymentRequired) {
		t.Fatalf("over spend cap: expected 402, got %v", err)
	}
}

// TestAsyncBuildDeployReGatedWhenCanceled asserts the async git build-deploy path
// re-checks billing/quota: a build that finishes AFTER the subscription is canceled
// must NOT deploy. The app is left "blocked" with the built image recorded, and the
// backend is never Applied.
func TestAsyncBuildDeployReGatedWhenCanceled(t *testing.T) {
	st := store.NewMemoryStore()
	kb := kube.NewFakeBackend()
	var wg sync.WaitGroup

	// The hook cancels the org's subscription mid-build, so by the time the build
	// succeeds the deploy gate (EnsureActive) must deny it.
	hb := &hookBuilder{
		image: "ghcr.io/acme/o/p/a:tag",
		hook: func() {
			_ = st.UpsertSubscription(context.Background(), &domain.Subscription{
				OrgID: "org-1", PlanID: "launch", Status: domain.SubCanceled,
				CurrentPeriodEnd: time.Now().AddDate(0, 1, 0),
			})
		},
	}
	svc := NewService(st, kb, billing.NewService(st, nil),
		WithBuilder(hb),
		WithBuildRegistry("ghcr.io/acme"),
		WithBuildWaitGroup(&wg),
	)
	ctx := context.Background()
	// Active at request time so CreateApp's gate passes and the build starts.
	seedSub(t, st, "org-1", "launch", domain.SubActive)

	app, err := svc.CreateApp(ctx, "org-1", CreateAppInput{
		Name: "web", GitRepository: "https://github.com/acme/web.git",
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	wg.Wait()

	got, _ := svc.GetApp(ctx, "org-1", app.ID)
	if got.Status != "blocked" {
		t.Fatalf("status = %q, want blocked (deploy re-gated after cancel)", got.Status)
	}
	if got.Release != "" {
		t.Fatalf("blocked app must not be deployed, got release %q", got.Release)
	}
	if got.Image != "ghcr.io/acme/o/p/a:tag" {
		t.Fatalf("built image must be recorded for a later deploy, got %q", got.Image)
	}
	if len(kb.Applied) != 0 {
		t.Fatalf("re-gated build must not Apply to the backend, got %d applies", len(kb.Applied))
	}
	// The build itself still succeeded.
	builds, _ := svc.ListBuilds(ctx, "org-1", app.ID, store.Page{})
	if len(builds) != 1 || builds[0].Status != "succeeded" {
		t.Fatalf("expected one succeeded build, got %+v", builds)
	}
}
