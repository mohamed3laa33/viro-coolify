package platform

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/catalog"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/coolify"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
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
// the template and the org's plan quota. In demo mode it stores a record with
// status "created"; when Coolify is configured it provisions via the call that
// matches the template's kind (database vs service vs app).
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
	svc := &domain.Service{
		ID:        s.idgen(),
		OrgID:     orgID,
		ProjectID: projectID,
		Template:  tmpl.Key,
		Name:      name,
		CPU:       cpu,
		MemoryMB:  memMB,
		Status:    "created",
		CreatedAt: s.now(),
	}

	if s.coolify.Configured() {
		var uuid string
		var err error
		switch catalog.Kind(tmpl.Kind) {
		case catalog.KindDatabase:
			uuid, err = s.coolify.CreateDatabase(ctx, coolify.CreateDatabaseRequest{
				Type:        tmpl.Key,
				Name:        name,
				ProjectUUID: in.ProjectUUID,
				ServerUUID:  in.ServerUUID,
			})
		default: // service & generic app
			uuid, err = s.coolify.CreateService(ctx, coolify.CreateServiceRequest{
				Type:        tmpl.Key,
				Name:        name,
				ProjectUUID: in.ProjectUUID,
				ServerUUID:  in.ServerUUID,
			})
		}
		if err != nil {
			return nil, err
		}
		svc.CoolifyUUID = uuid
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

// kindOf returns the catalog kind for a service's template (KindService default).
func (s *Service) kindOf(ctx context.Context, template string) catalog.Kind {
	if t, ok := s.templateByKey(ctx, template); ok {
		return catalog.Kind(t.Kind)
	}
	return catalog.KindService
}

// serviceAction applies a status transition, calling the matching Coolify
// lifecycle method for the service's kind when configured and provisioned.
func (s *Service) serviceAction(ctx context.Context, orgID, serviceID, status string,
	svcFn, dbFn func(context.Context, string) error) (*domain.Service, error) {
	svc, err := s.ownedService(ctx, orgID, serviceID)
	if err != nil {
		return nil, err
	}
	if s.coolify.Configured() && svc.CoolifyUUID != "" {
		fn := svcFn
		if s.kindOf(ctx, svc.Template) == catalog.KindDatabase {
			fn = dbFn
		}
		if err := fn(ctx, svc.CoolifyUUID); err != nil {
			return nil, err
		}
	}
	svc.Status = status
	if err := s.store.UpdateService(ctx, svc); err != nil {
		return nil, err
	}
	return svc, nil
}

// DeployService starts/deploys a service.
func (s *Service) DeployService(ctx context.Context, orgID, serviceID string) (*domain.Service, error) {
	return s.serviceAction(ctx, orgID, serviceID, "deploying", s.coolify.StartService, s.coolify.StartDatabase)
}

// StopService stops a service.
func (s *Service) StopService(ctx context.Context, orgID, serviceID string) (*domain.Service, error) {
	return s.serviceAction(ctx, orgID, serviceID, "stopped", s.coolify.StopService, s.coolify.StopDatabase)
}

// RestartService restarts a service.
func (s *Service) RestartService(ctx context.Context, orgID, serviceID string) (*domain.Service, error) {
	return s.serviceAction(ctx, orgID, serviceID, "restarting", s.coolify.RestartService, s.coolify.RestartDatabase)
}

// DeleteService removes a service from Coolify (when configured) and the store.
func (s *Service) DeleteService(ctx context.Context, orgID, serviceID string) error {
	svc, err := s.ownedService(ctx, orgID, serviceID)
	if err != nil {
		return err
	}
	if s.coolify.Configured() && svc.CoolifyUUID != "" {
		if s.kindOf(ctx, svc.Template) == catalog.KindDatabase {
			err = s.coolify.DeleteDatabase(ctx, svc.CoolifyUUID)
		} else {
			err = s.coolify.DeleteService(ctx, svc.CoolifyUUID)
		}
		if err != nil {
			return err
		}
	}
	return s.store.DeleteService(ctx, svc.ID)
}
