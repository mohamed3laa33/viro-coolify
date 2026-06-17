package platform

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/catalog"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/kube"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/store"
)

// ListCatalog returns the active one-click templates, sorted by SortOrder, from
// the store.
func (s *Service) ListCatalog(ctx context.Context) []domain.ServiceTemplate {
	tmpls, err := s.store.ListServiceTemplates(ctx)
	if err != nil {
		return nil
	}
	active := make([]domain.ServiceTemplate, 0, len(tmpls))
	for _, t := range tmpls {
		if t.Active {
			active = append(active, t)
		}
	}
	sort.Slice(active, func(i, j int) bool { return active[i].SortOrder < active[j].SortOrder })
	return active
}

// templateByKey looks up a stored template by key.
func (s *Service) templateByKey(ctx context.Context, key string) (domain.ServiceTemplate, bool) {
	t, err := s.store.GetServiceTemplate(ctx, key)
	if err != nil {
		return domain.ServiceTemplate{}, false
	}
	return *t, true
}

// CreateServiceInput describes a new one-click service.
type CreateServiceInput struct {
	TemplateKey string
	Name        string
	CPU         float64
	MemoryMB    int
	ProjectUUID string // Coolify project placement (optional)
	ServerUUID  string
}

// CreateService provisions a one-click catalog instance for the org, validating
// the template and the org's plan quota, then deploying it onto the Kubernetes
// backend: it ensures the per-org-project tenant namespace/quota and installs
// the workload as a Helm release. The resulting placement (namespace/release/
// host) is persisted and the service is marked "deploying".
func (s *Service) CreateService(ctx context.Context, orgID, projectID string, in CreateServiceInput) (*domain.Service, error) {
	tmpl, ok := s.templateByKey(ctx, in.TemplateKey)
	if !ok {
		return nil, ErrInvalidTemplate
	}
	cpu, memMB := s.normalizeResources(ctx, in.CPU, in.MemoryMB)

	count, err := s.workloadCount(ctx, orgID)
	if err != nil {
		return nil, err
	}
	if err := s.checkQuota(ctx, orgID, cpu, memMB, count); err != nil {
		return nil, err
	}

	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = tmpl.Name
	}

	orgSlug := s.orgSlug(ctx, orgID)
	projSlug := s.projectSlug(ctx, projectID)

	namespace, err := s.backend.EnsureTenant(ctx, orgSlug, projSlug, s.quotaForOrg(ctx, orgID))
	if err != nil {
		return nil, err
	}

	kind := "service"
	if catalog.Kind(tmpl.Kind) == catalog.KindDatabase {
		kind = "database"
	}

	release, host, err := s.backend.Apply(ctx, kube.Workload{
		OrgSlug:            orgSlug,
		ProjectSlug:        projSlug,
		Name:               name,
		Kind:               kind,
		Image:              tmpl.Image,
		CPU:                cpu,
		MemoryMB:           memMB,
		ServiceTemplateKey: tmpl.Key,
	})
	if err != nil {
		return nil, err
	}

	svc := &domain.Service{
		ID:        s.idgen(),
		OrgID:     orgID,
		ProjectID: projectID,
		Template:  tmpl.Key,
		Name:      name,
		CPU:       cpu,
		MemoryMB:  memMB,
		Status:    "deploying",
		Namespace: namespace,
		Release:   release,
		Host:      host,
		CreatedAt: s.now(),
	}

	if err := s.store.CreateService(ctx, svc); err != nil {
		return nil, err
	}
	return svc, nil
}

// ListServices returns the services belonging to the org.
func (s *Service) ListServices(ctx context.Context, orgID string) ([]domain.Service, error) {
	return s.store.ListServicesByOrg(ctx, orgID)
}

// GetService returns one service scoped to the org.
func (s *Service) GetService(ctx context.Context, orgID, serviceID string) (*domain.Service, error) {
	return s.ownedService(ctx, orgID, serviceID)
}

func (s *Service) ownedService(ctx context.Context, orgID, serviceID string) (*domain.Service, error) {
	svc, err := s.store.GetService(ctx, serviceID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if svc.OrgID != orgID {
		return nil, ErrNotFound
	}
	return svc, nil
}

// serviceAction applies a status transition, invoking the backend lifecycle
// call for the service's release when it has been deployed.
func (s *Service) serviceAction(ctx context.Context, orgID, serviceID, status string,
	fn func(context.Context, string, string) error) (*domain.Service, error) {
	svc, err := s.ownedService(ctx, orgID, serviceID)
	if err != nil {
		return nil, err
	}
	if svc.Release != "" {
		if err := fn(ctx, svc.Namespace, svc.Release); err != nil {
			return nil, err
		}
	}
	svc.Status = status
	if err := s.store.UpdateService(ctx, svc); err != nil {
		return nil, err
	}
	return svc, nil
}

// DeployService (re)starts a service on the backend.
func (s *Service) DeployService(ctx context.Context, orgID, serviceID string) (*domain.Service, error) {
	return s.serviceAction(ctx, orgID, serviceID, "deploying", s.backend.Start)
}

// StopService scales a service to zero on the backend.
func (s *Service) StopService(ctx context.Context, orgID, serviceID string) (*domain.Service, error) {
	return s.serviceAction(ctx, orgID, serviceID, "stopped", s.backend.Stop)
}

// RestartService triggers a rollout restart of a service on the backend.
func (s *Service) RestartService(ctx context.Context, orgID, serviceID string) (*domain.Service, error) {
	return s.serviceAction(ctx, orgID, serviceID, "restarting", s.backend.Restart)
}

// DeleteService uninstalls a service's release from the backend (when deployed)
// and removes the store record.
func (s *Service) DeleteService(ctx context.Context, orgID, serviceID string) error {
	svc, err := s.ownedService(ctx, orgID, serviceID)
	if err != nil {
		return err
	}
	if svc.Release != "" {
		if err := s.backend.Delete(ctx, svc.Namespace, svc.Release); err != nil {
			return err
		}
	}
	return s.store.DeleteService(ctx, svc.ID)
}
