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

// templateFailStore makes the service-template reads fail, so we can assert that
// ListCatalog PROPAGATES a transient store error rather than swallowing it as an
// empty catalog (which would make the public catalog endpoint a misleading 200).
type templateFailStore struct {
	store.Store
	err error
}

func (s templateFailStore) ListServiceTemplates(context.Context) ([]domain.ServiceTemplate, error) {
	return nil, s.err
}

func TestListCatalogPropagatesStoreError(t *testing.T) {
	boom := errors.New("transient db failure")
	st := templateFailStore{Store: store.NewMemoryStore(), err: boom}
	svc := NewService(st, kube.NewFakeBackend(), billing.NewService(st, nil))

	if _, err := svc.ListCatalog(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("ListCatalog err = %v, want %v", err, boom)
	}
}
