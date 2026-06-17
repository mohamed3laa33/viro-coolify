package store

import (
	"context"
	"strings"
	"sync"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
)

// MemoryStore is a thread-safe, in-memory Store for local development and tests.
type MemoryStore struct {
	mu             sync.RWMutex
	users          map[string]domain.User // by id
	usersByEmail   map[string]string      // email -> id
	organizations  map[string]domain.Organization
	memberships    map[string]domain.Membership        // key: orgID + "\x00" + userID
	apps           map[string]domain.App               // by id
	databases      map[string]domain.Database          // by id
	subscriptions  map[string]domain.Subscription      // by orgID
	usage          map[string][]domain.UsageRecord     // by orgID
	projects       map[string]domain.Project           // by id
	projectMembers map[string]domain.ProjectMembership // key: projectID + "\x00" + userID
	invitations    map[string]domain.Invitation        // by id
	services       map[string]domain.Service           // by id
	appEnv         map[string]map[string]string        // appID -> key -> value
	domains        map[string]domain.Domain            // by id
	plans          map[string]domain.Plan              // by id
	templates      map[string]domain.ServiceTemplate   // by key
	settings       domain.PlatformSettings             // singleton
}

// NewMemoryStore returns an in-memory store seeded with the default business
// config (plans, service templates and platform settings).
func NewMemoryStore() *MemoryStore {
	s := &MemoryStore{
		users:          make(map[string]domain.User),
		usersByEmail:   make(map[string]string),
		organizations:  make(map[string]domain.Organization),
		memberships:    make(map[string]domain.Membership),
		apps:           make(map[string]domain.App),
		databases:      make(map[string]domain.Database),
		subscriptions:  make(map[string]domain.Subscription),
		usage:          make(map[string][]domain.UsageRecord),
		projects:       make(map[string]domain.Project),
		projectMembers: make(map[string]domain.ProjectMembership),
		invitations:    make(map[string]domain.Invitation),
		services:       make(map[string]domain.Service),
		appEnv:         make(map[string]map[string]string),
		domains:        make(map[string]domain.Domain),
		plans:          make(map[string]domain.Plan),
		templates:      make(map[string]domain.ServiceTemplate),
	}
	s.seed()
	return s
}

// seed populates the default business config when empty. It is idempotent.
func (s *MemoryStore) seed() {
	if len(s.plans) == 0 {
		for _, p := range defaultPlans() {
			s.plans[p.ID] = p
		}
	}
	if len(s.templates) == 0 {
		for _, t := range defaultTemplates() {
			s.templates[t.Key] = t
		}
	}
	if s.settings.DefaultPlanID == "" {
		s.settings = defaultSettings()
	}
}

// defaultPlans returns the seeded billing catalog (prices + per-plan quotas).
func defaultPlans() []domain.Plan {
	return []domain.Plan{
		{
			ID: "hobby", Name: "Hobby",
			Description: "For side projects. Shared CPU, 1 app, community support.",
			PriceCents:  0, Currency: "usd",
			IncludedHours: 160, OveragePerHourCents: 0,
			MaxCPU: 0.5, MaxMemoryMB: 512, MaxApps: 3,
			IsDefault: true, SortOrder: 1, Active: true,
		},
		{
			ID: "launch", Name: "Launch",
			Description: "For production apps. Dedicated CPU, autoscaling, custom domains.",
			PriceCents:  2900, Currency: "usd",
			IncludedHours: 720, OveragePerHourCents: 2,
			MaxCPU: 1, MaxMemoryMB: 1024, MaxApps: 20,
			IsDefault: false, SortOrder: 2, Active: true,
		},
		{
			ID: "scale", Name: "Scale",
			Description: "For scaling teams. Multi-region, higher limits, priority support.",
			PriceCents:  9900, Currency: "usd",
			IncludedHours: 2400, OveragePerHourCents: 1,
			MaxCPU: 2, MaxMemoryMB: 4096, MaxApps: 100,
			IsDefault: false, SortOrder: 3, Active: true,
		},
	}
}

// defaultTemplates returns the seeded one-click catalog.
func defaultTemplates() []domain.ServiceTemplate {
	return []domain.ServiceTemplate{
		{Key: "wordpress", Name: "WordPress", Description: "The world's most popular CMS.", Category: "CMS", Kind: "service", Image: "wordpress:6.8-php8.3-apache", DefaultPort: 80, Active: true, SortOrder: 1},
		{Key: "ghost", Name: "Ghost", Description: "Modern publishing platform.", Category: "CMS", Kind: "service", Image: "ghost:5-alpine", DefaultPort: 2368, Active: true, SortOrder: 2},
		{Key: "plausible", Name: "Plausible", Description: "Privacy-friendly web analytics.", Category: "Analytics", Kind: "service", Image: "plausible/analytics:v2.1.0", DefaultPort: 8000, Active: true, SortOrder: 3},
		{Key: "n8n", Name: "n8n", Description: "Workflow automation.", Category: "Automation", Kind: "service", Image: "n8nio/n8n:1.64.0", DefaultPort: 5678, Active: true, SortOrder: 4},
		{Key: "postgresql", Name: "PostgreSQL", Description: "Relational database.", Category: "Database", Kind: "database", Image: "postgres:16-alpine", DefaultPort: 5432, Active: true, SortOrder: 5},
		{Key: "mysql", Name: "MySQL", Description: "Relational database.", Category: "Database", Kind: "database", Image: "mysql:8.4", DefaultPort: 3306, Active: true, SortOrder: 6},
		{Key: "mariadb", Name: "MariaDB", Description: "MySQL-compatible relational database.", Category: "Database", Kind: "database", Image: "mariadb:11", DefaultPort: 3306, Active: true, SortOrder: 7},
		{Key: "mongodb", Name: "MongoDB", Description: "Document database.", Category: "Database", Kind: "database", Image: "mongo:7", DefaultPort: 27017, Active: true, SortOrder: 8},
		{Key: "redis", Name: "Redis", Description: "In-memory key-value store.", Category: "Database", Kind: "database", Image: "redis:7-alpine", DefaultPort: 6379, Active: true, SortOrder: 9},
		{Key: "docker-image", Name: "Docker Image", Description: "Deploy any public Docker image.", Category: "App", Kind: "app", Image: "", DefaultPort: 80, Active: true, SortOrder: 10},
	}
}

// DefaultSettings returns the seeded platform settings (the same defaults used
// to seed the store). Exposed so boot-time consumers (e.g. the deploy backend)
// can read the default overcommit factors before any DB read.
func DefaultSettings() domain.PlatformSettings { return defaultSettings() }

// defaultSettings returns the seeded platform settings.
func defaultSettings() domain.PlatformSettings {
	// Start minimal: a new workload defaults to a small footprint and grows up to
	// the plan's Max* ceilings. All of these are admin-editable via /v1/admin/settings.
	return domain.PlatformSettings{
		DefaultCPU:             0.1, // 100m
		DefaultMemoryMB:        128,
		DefaultPlanID:          "hobby",
		CPUOvercommitFactor:    0.2,
		MemoryOvercommitFactor: 0.35,
		DefaultRegion:          "fra1",
		Regions:                []string{"fra1", "nyc1", "sfo3", "sgp1"},
	}
}

var _ Store = (*MemoryStore)(nil)

func membershipKey(orgID, userID string) string { return orgID + "\x00" + userID }

func (s *MemoryStore) CreateUser(_ context.Context, u *domain.User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	email := strings.ToLower(u.Email)
	if _, exists := s.usersByEmail[email]; exists {
		return ErrConflict
	}
	if _, exists := s.users[u.ID]; exists {
		return ErrConflict
	}
	s.users[u.ID] = *u
	s.usersByEmail[email] = u.ID
	return nil
}

func (s *MemoryStore) GetUserByID(_ context.Context, id string) (*domain.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.users[id]
	if !ok {
		return nil, ErrNotFound
	}
	return &u, nil
}

func (s *MemoryStore) UpdateUser(_ context.Context, u *domain.User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.users[u.ID]
	if !ok {
		return ErrNotFound
	}
	// Email is the stable lookup key; do not allow it to change here.
	u.Email = existing.Email
	s.users[u.ID] = *u
	return nil
}

func (s *MemoryStore) GetUserByEmail(_ context.Context, email string) (*domain.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.usersByEmail[strings.ToLower(email)]
	if !ok {
		return nil, ErrNotFound
	}
	u := s.users[id]
	return &u, nil
}

func (s *MemoryStore) CreateOrganization(_ context.Context, o *domain.Organization) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.organizations[o.ID]; exists {
		return ErrConflict
	}
	s.organizations[o.ID] = *o
	return nil
}

func (s *MemoryStore) GetOrganization(_ context.Context, id string) (*domain.Organization, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	o, ok := s.organizations[id]
	if !ok {
		return nil, ErrNotFound
	}
	return &o, nil
}

func (s *MemoryStore) ListOrganizationsForUser(_ context.Context, userID string) ([]domain.Organization, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []domain.Organization
	for _, m := range s.memberships {
		if m.UserID == userID {
			if o, ok := s.organizations[m.OrgID]; ok {
				out = append(out, o)
			}
		}
	}
	return out, nil
}

func (s *MemoryStore) AddMembership(_ context.Context, m domain.Membership) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := membershipKey(m.OrgID, m.UserID)
	if _, exists := s.memberships[key]; exists {
		return ErrConflict
	}
	s.memberships[key] = m
	return nil
}

func (s *MemoryStore) GetMembership(_ context.Context, orgID, userID string) (*domain.Membership, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.memberships[membershipKey(orgID, userID)]
	if !ok {
		return nil, ErrNotFound
	}
	return &m, nil
}

func (s *MemoryStore) ListMemberships(_ context.Context, orgID string) ([]domain.Membership, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []domain.Membership
	for _, m := range s.memberships {
		if m.OrgID == orgID {
			out = append(out, m)
		}
	}
	return out, nil
}

func (s *MemoryStore) CreateApp(_ context.Context, a *domain.App) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.apps[a.ID]; exists {
		return ErrConflict
	}
	s.apps[a.ID] = *a
	return nil
}

func (s *MemoryStore) GetApp(_ context.Context, id string) (*domain.App, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.apps[id]
	if !ok {
		return nil, ErrNotFound
	}
	return &a, nil
}

func (s *MemoryStore) ListAppsByOrg(_ context.Context, orgID string) ([]domain.App, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.App, 0)
	for _, a := range s.apps {
		if a.OrgID == orgID {
			out = append(out, a)
		}
	}
	return out, nil
}

func (s *MemoryStore) UpdateApp(_ context.Context, a *domain.App) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.apps[a.ID]; !ok {
		return ErrNotFound
	}
	s.apps[a.ID] = *a
	return nil
}

func (s *MemoryStore) DeleteApp(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.apps[id]; !ok {
		return ErrNotFound
	}
	delete(s.apps, id)
	return nil
}

func (s *MemoryStore) CreateDatabase(_ context.Context, d *domain.Database) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.databases[d.ID]; exists {
		return ErrConflict
	}
	s.databases[d.ID] = *d
	return nil
}

func (s *MemoryStore) GetDatabase(_ context.Context, id string) (*domain.Database, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.databases[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := d
	return &cp, nil
}

func (s *MemoryStore) ListDatabasesByOrg(_ context.Context, orgID string) ([]domain.Database, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.Database, 0)
	for _, d := range s.databases {
		if d.OrgID == orgID {
			out = append(out, d)
		}
	}
	return out, nil
}

func (s *MemoryStore) DeleteDatabase(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.databases[id]; !ok {
		return ErrNotFound
	}
	delete(s.databases, id)
	return nil
}

func (s *MemoryStore) CreateProject(_ context.Context, p *domain.Project) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.projects[p.ID]; exists {
		return ErrConflict
	}
	s.projects[p.ID] = *p
	return nil
}

func (s *MemoryStore) GetProject(_ context.Context, id string) (*domain.Project, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.projects[id]
	if !ok {
		return nil, ErrNotFound
	}
	return &p, nil
}

func (s *MemoryStore) ListProjectsByOrg(_ context.Context, orgID string) ([]domain.Project, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.Project, 0)
	for _, p := range s.projects {
		if p.OrgID == orgID {
			out = append(out, p)
		}
	}
	return out, nil
}

func projectMemberKey(projectID, userID string) string { return projectID + "\x00" + userID }

func (s *MemoryStore) AddProjectMembership(_ context.Context, m domain.ProjectMembership) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := projectMemberKey(m.ProjectID, m.UserID)
	if _, exists := s.projectMembers[key]; exists {
		return ErrConflict
	}
	s.projectMembers[key] = m
	return nil
}

func (s *MemoryStore) GetProjectMembership(_ context.Context, projectID, userID string) (*domain.ProjectMembership, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.projectMembers[projectMemberKey(projectID, userID)]
	if !ok {
		return nil, ErrNotFound
	}
	return &m, nil
}

func (s *MemoryStore) CreateInvitation(_ context.Context, inv *domain.Invitation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.invitations[inv.ID]; exists {
		return ErrConflict
	}
	s.invitations[inv.ID] = *inv
	return nil
}

func (s *MemoryStore) GetInvitationByToken(_ context.Context, token string) (*domain.Invitation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, inv := range s.invitations {
		if inv.Token == token {
			return &inv, nil
		}
	}
	return nil, ErrNotFound
}

func (s *MemoryStore) ListInvitationsByOrg(_ context.Context, orgID string) ([]domain.Invitation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.Invitation, 0)
	for _, inv := range s.invitations {
		if inv.OrgID == orgID {
			out = append(out, inv)
		}
	}
	return out, nil
}

func (s *MemoryStore) UpdateInvitation(_ context.Context, inv *domain.Invitation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.invitations[inv.ID]; !ok {
		return ErrNotFound
	}
	s.invitations[inv.ID] = *inv
	return nil
}

func (s *MemoryStore) UpsertSubscription(_ context.Context, sub *domain.Subscription) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subscriptions[sub.OrgID] = *sub
	return nil
}

func (s *MemoryStore) GetSubscription(_ context.Context, orgID string) (*domain.Subscription, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sub, ok := s.subscriptions[orgID]
	if !ok {
		return nil, ErrNotFound
	}
	return &sub, nil
}

func (s *MemoryStore) AddUsage(_ context.Context, u *domain.UsageRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.usage[u.OrgID] = append(s.usage[u.OrgID], *u)
	return nil
}

func (s *MemoryStore) ListUsageByOrg(_ context.Context, orgID string) ([]domain.UsageRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.UsageRecord, len(s.usage[orgID]))
	copy(out, s.usage[orgID])
	return out, nil
}

func (s *MemoryStore) CreateService(_ context.Context, svc *domain.Service) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.services[svc.ID]; exists {
		return ErrConflict
	}
	s.services[svc.ID] = *svc
	return nil
}

func (s *MemoryStore) GetService(_ context.Context, id string) (*domain.Service, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	svc, ok := s.services[id]
	if !ok {
		return nil, ErrNotFound
	}
	return &svc, nil
}

func (s *MemoryStore) ListServicesByOrg(_ context.Context, orgID string) ([]domain.Service, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.Service, 0)
	for _, svc := range s.services {
		if svc.OrgID == orgID {
			out = append(out, svc)
		}
	}
	return out, nil
}

func (s *MemoryStore) UpdateService(_ context.Context, svc *domain.Service) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.services[svc.ID]; !ok {
		return ErrNotFound
	}
	s.services[svc.ID] = *svc
	return nil
}

func (s *MemoryStore) DeleteService(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.services[id]; !ok {
		return ErrNotFound
	}
	delete(s.services, id)
	return nil
}

func (s *MemoryStore) GetAppEnv(_ context.Context, appID string) (map[string]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string, len(s.appEnv[appID]))
	for k, v := range s.appEnv[appID] {
		out[k] = v
	}
	return out, nil
}

func (s *MemoryStore) SetAppEnv(_ context.Context, appID, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.appEnv[appID] == nil {
		s.appEnv[appID] = make(map[string]string)
	}
	s.appEnv[appID][key] = value
	return nil
}

func (s *MemoryStore) DeleteAppEnv(_ context.Context, appID, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m := s.appEnv[appID]; m != nil {
		delete(m, key)
	}
	return nil
}

func (s *MemoryStore) CreateDomain(_ context.Context, d *domain.Domain) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.domains[d.ID]; exists {
		return ErrConflict
	}
	s.domains[d.ID] = *d
	return nil
}

func (s *MemoryStore) GetDomain(_ context.Context, id string) (*domain.Domain, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.domains[id]
	if !ok {
		return nil, ErrNotFound
	}
	return &d, nil
}

func (s *MemoryStore) ListDomainsByApp(_ context.Context, appID string) ([]domain.Domain, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.Domain, 0)
	for _, d := range s.domains {
		if d.AppID == appID {
			out = append(out, d)
		}
	}
	return out, nil
}

func (s *MemoryStore) DeleteDomain(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.domains[id]; !ok {
		return ErrNotFound
	}
	delete(s.domains, id)
	return nil
}

// ---- Plans ----

func (s *MemoryStore) ListPlans(_ context.Context) ([]domain.Plan, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.Plan, 0, len(s.plans))
	for _, p := range s.plans {
		out = append(out, p)
	}
	return out, nil
}

func (s *MemoryStore) GetPlan(_ context.Context, id string) (*domain.Plan, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.plans[id]
	if !ok {
		return nil, ErrNotFound
	}
	return &p, nil
}

func (s *MemoryStore) UpsertPlan(_ context.Context, p *domain.Plan) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.plans[p.ID] = *p
	return nil
}

func (s *MemoryStore) DeletePlan(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.plans[id]; !ok {
		return ErrNotFound
	}
	delete(s.plans, id)
	return nil
}

// ---- Service templates ----

func (s *MemoryStore) ListServiceTemplates(_ context.Context) ([]domain.ServiceTemplate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.ServiceTemplate, 0, len(s.templates))
	for _, t := range s.templates {
		out = append(out, t)
	}
	return out, nil
}

func (s *MemoryStore) GetServiceTemplate(_ context.Context, key string) (*domain.ServiceTemplate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.templates[key]
	if !ok {
		return nil, ErrNotFound
	}
	return &t, nil
}

func (s *MemoryStore) UpsertServiceTemplate(_ context.Context, t *domain.ServiceTemplate) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.templates[t.Key] = *t
	return nil
}

func (s *MemoryStore) DeleteServiceTemplate(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.templates[key]; !ok {
		return ErrNotFound
	}
	delete(s.templates, key)
	return nil
}

// ---- Platform settings (singleton) ----

func (s *MemoryStore) GetSettings(_ context.Context) (*domain.PlatformSettings, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	settings := s.settings
	// Return a copy of the Regions slice so callers cannot mutate internal state.
	settings.Regions = append([]string(nil), s.settings.Regions...)
	return &settings, nil
}

func (s *MemoryStore) UpdateSettings(_ context.Context, in *domain.PlatformSettings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	settings := *in
	settings.Regions = append([]string(nil), in.Regions...)
	s.settings = settings
	return nil
}

// ---- Admin overview helpers ----

func (s *MemoryStore) ListAllOrgs(_ context.Context) ([]domain.Organization, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.Organization, 0, len(s.organizations))
	for _, o := range s.organizations {
		out = append(out, o)
	}
	return out, nil
}

func (s *MemoryStore) CountUsers(_ context.Context) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.users), nil
}

func (s *MemoryStore) ListAllSubscriptions(_ context.Context) ([]domain.Subscription, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.Subscription, 0, len(s.subscriptions))
	for _, sub := range s.subscriptions {
		out = append(out, sub)
	}
	return out, nil
}

func (s *MemoryStore) ListAllUsage(_ context.Context) ([]domain.UsageRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.UsageRecord, 0)
	for _, recs := range s.usage {
		out = append(out, recs...)
	}
	return out, nil
}
