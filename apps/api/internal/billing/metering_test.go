package billing

import (
	"context"
	"errors"
	"testing"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/store"
)

// addUsageFailStore fails AddUsage for one specific org, so we can assert that
// MeterUsage is continue-on-error (one bad org doesn't abort the rest).
type addUsageFailStore struct {
	store.Store
	failOrg string
}

func (s addUsageFailStore) AddUsage(ctx context.Context, u *domain.UsageRecord) error {
	if u.OrgID == s.failOrg {
		return errors.New("boom")
	}
	return s.Store.AddUsage(ctx, u)
}

func TestMeterUsageContinuesOnError(t *testing.T) {
	mem := store.NewMemoryStore()
	st := addUsageFailStore{Store: mem, failOrg: "bad"}
	ctx := context.Background()

	for _, id := range []string{"bad", "good"} {
		if err := mem.CreateOrganization(ctx, &domain.Organization{ID: id, Slug: id}); err != nil {
			t.Fatalf("create org %s: %v", id, err)
		}
		mustApp(t, mem, &domain.App{ID: "app-" + id, OrgID: id, Name: id, CPU: 4, MemoryMB: 4096, Status: "running", Release: "app-" + id})
	}
	setPrices(t, mem, 0.01, 0.001)

	svc := NewService(st, nil)
	n, err := svc.MeterUsage(ctx)
	if err == nil {
		t.Fatal("expected a non-nil first error from the failing org")
	}
	if n != 1 {
		t.Fatalf("metered orgs = %d, want 1 (the good org despite the bad one)", n)
	}
	recs, _ := mem.ListUsageByOrg(ctx, "good")
	if len(recs) == 0 {
		t.Fatal("expected the good org to be metered despite the bad org failing")
	}
}
