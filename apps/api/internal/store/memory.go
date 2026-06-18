package store

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
)

// txMutex is an RWMutex that can be put into a "held" mode by WithTx. While held,
// the per-method Lock/Unlock/RLock/RUnlock calls become no-ops so the existing
// store methods can run unchanged inside a transaction without self-deadlocking
// (the WithTx caller already holds the real write lock for the whole closure).
// It is NOT a general-purpose reentrant lock: only WithTx flips held, and only
// while it owns the underlying write lock on a single goroutine.
type txMutex struct {
	rw   sync.RWMutex
	held bool // true only between WithTx's Lock and Unlock, on the tx goroutine
}

func (m *txMutex) Lock() {
	if m.held {
		return
	}
	m.rw.Lock()
}

func (m *txMutex) Unlock() {
	if m.held {
		return
	}
	m.rw.Unlock()
}

func (m *txMutex) RLock() {
	if m.held {
		return
	}
	m.rw.RLock()
}

func (m *txMutex) RUnlock() {
	if m.held {
		return
	}
	m.rw.RUnlock()
}

// MemoryStore is a thread-safe, in-memory Store for local development and tests.
type MemoryStore struct {
	mu             txMutex
	users          map[string]domain.User // by id
	usersByEmail   map[string]string      // email -> id
	organizations  map[string]domain.Organization
	memberships    map[string]domain.Membership         // key: orgID + "\x00" + userID
	apps           map[string]domain.App                // by id
	builds         map[string]domain.Build              // by id
	databases      map[string]domain.Database           // by id
	subscriptions  map[string]domain.Subscription       // by orgID
	usage          map[string][]domain.UsageRecord      // by orgID
	projects       map[string]domain.Project            // by id
	projectMembers map[string]domain.ProjectMembership  // key: projectID + "\x00" + userID
	invitations    map[string]domain.Invitation         // by id
	services       map[string]domain.Service            // by id
	appEnv         map[string]map[string]envEntry       // appID -> key -> entry
	auditEvents    []domain.AuditEvent                  // append-only audit log
	domains        map[string]domain.Domain             // by id
	plans          map[string]domain.Plan               // by id
	templates      map[string]domain.ServiceTemplate    // by key
	pricing        map[string]domain.PricingComponent   // by key
	refreshTokens  map[string]domain.RefreshToken       // by jti
	resetTokens    map[string]domain.PasswordResetToken // by id
	settings       domain.PlatformSettings              // singleton
	processedEvts  map[string]struct{}                  // stripe event id dedupe
	meterState     *domain.MeterState                   // metering progress (singleton)
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
		builds:         make(map[string]domain.Build),
		databases:      make(map[string]domain.Database),
		subscriptions:  make(map[string]domain.Subscription),
		usage:          make(map[string][]domain.UsageRecord),
		projects:       make(map[string]domain.Project),
		projectMembers: make(map[string]domain.ProjectMembership),
		invitations:    make(map[string]domain.Invitation),
		services:       make(map[string]domain.Service),
		appEnv:         make(map[string]map[string]envEntry),
		domains:        make(map[string]domain.Domain),
		plans:          make(map[string]domain.Plan),
		templates:      make(map[string]domain.ServiceTemplate),
		pricing:        make(map[string]domain.PricingComponent),
		refreshTokens:  make(map[string]domain.RefreshToken),
		resetTokens:    make(map[string]domain.PasswordResetToken),
		processedEvts:  make(map[string]struct{}),
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
	if len(s.pricing) == 0 {
		for _, p := range defaultPricing() {
			s.pricing[p.Key] = p
		}
	}
	if s.settings.DefaultPlanID == "" {
		s.settings = defaultSettings()
	}
}

// defaultPricing seeds the billable component CATALOG only — which resources are
// metered (cpu/memory/storage) and their units. Prices are seeded at 0 on
// purpose: the platform never invents prices. The admin sets the real
// PricePerHour via the super-admin pricing API; until then a component is free.
func defaultPricing() []domain.PricingComponent {
	return []domain.PricingComponent{
		{Key: "cpu", Name: "vCPU", Unit: "vCPU-hour", PricePerHour: 0, Currency: "usd", Active: true, SortOrder: 1},
		{Key: "memory", Name: "Memory", Unit: "GB-hour", PricePerHour: 0, Currency: "usd", Active: true, SortOrder: 2},
		{Key: "storage", Name: "Storage", Unit: "GB-hour", PricePerHour: 0, Currency: "usd", Active: true, SortOrder: 3},
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

// WithTx runs fn under the write lock for serialization. The in-memory store has
// NO real transaction: a mid-fn failure does NOT roll back writes already applied
// by fn. It exists so call sites can share one code path with the Postgres store
// (which is truly atomic) and so concurrent multi-write sequences are serialized.
// The Store passed to fn is the same store with its lock marked held, so nested
// method calls do not self-deadlock.
func (s *MemoryStore) WithTx(ctx context.Context, fn func(tx Store) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.rw.Lock()
	s.mu.held = true
	defer func() {
		s.mu.held = false
		s.mu.rw.Unlock()
	}()
	return fn(s)
}

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

// UpdateOrg persists the mutable org fields (name, billing email). The org ID
// is the stable lookup key and is not changed here.
func (s *MemoryStore) UpdateOrg(_ context.Context, o *domain.Organization) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.organizations[o.ID]
	if !ok {
		return ErrNotFound
	}
	existing.Name = o.Name
	existing.BillingEmail = o.BillingEmail
	existing.SpendCapCents = o.SpendCapCents
	s.organizations[o.ID] = existing
	*o = existing
	return nil
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

func (s *MemoryStore) UpdateMembershipRole(_ context.Context, orgID, userID string, role domain.Role) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := membershipKey(orgID, userID)
	m, ok := s.memberships[key]
	if !ok {
		return ErrNotFound
	}
	m.Role = role
	s.memberships[key] = m
	return nil
}

func (s *MemoryStore) RemoveMembership(_ context.Context, orgID, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := membershipKey(orgID, userID)
	if _, ok := s.memberships[key]; !ok {
		return ErrNotFound
	}
	delete(s.memberships, key)
	return nil
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

// ---- Builds ----

func (s *MemoryStore) CreateBuild(_ context.Context, b *domain.Build) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.builds[b.ID]; exists {
		return ErrConflict
	}
	s.builds[b.ID] = *b
	return nil
}

func (s *MemoryStore) GetBuild(_ context.Context, id string) (*domain.Build, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.builds[id]
	if !ok {
		return nil, ErrNotFound
	}
	return &b, nil
}

func (s *MemoryStore) ListBuildsByApp(_ context.Context, appID string) ([]domain.Build, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.Build, 0)
	for _, b := range s.builds {
		if b.AppID == appID {
			out = append(out, b)
		}
	}
	// Newest first, so the list endpoint shows the latest build at the top.
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (s *MemoryStore) UpdateBuild(_ context.Context, b *domain.Build) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.builds[b.ID]; !ok {
		return ErrNotFound
	}
	s.builds[b.ID] = *b
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

func (s *MemoryStore) UpdateDatabase(_ context.Context, d *domain.Database) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.databases[d.ID]; !ok {
		return ErrNotFound
	}
	s.databases[d.ID] = *d
	return nil
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

// Close is a no-op for the in-memory store.
func (s *MemoryStore) Close() {}

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

// DeleteProject removes an empty project scoped to orgID. It returns ErrNotFound
// when the project does not exist within the org, and ErrConflict when the
// project still owns any apps or services.
func (s *MemoryStore) DeleteProject(_ context.Context, orgID, projectID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.projects[projectID]
	if !ok || p.OrgID != orgID {
		return ErrNotFound
	}
	for _, a := range s.apps {
		if a.ProjectID == projectID {
			return ErrConflict
		}
	}
	for _, svc := range s.services {
		if svc.ProjectID == projectID {
			return ErrConflict
		}
	}
	delete(s.projects, projectID)
	return nil
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

// RevokeInvitation marks an org's invitation as revoked. It returns ErrNotFound
// when no matching invitation exists within the org.
func (s *MemoryStore) RevokeInvitation(_ context.Context, orgID, inviteID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	inv, ok := s.invitations[inviteID]
	if !ok || inv.OrgID != orgID {
		return ErrNotFound
	}
	inv.Status = domain.InviteRevoked
	s.invitations[inviteID] = inv
	return nil
}

// ---- Refresh tokens (rotation + revocation) ----

func (s *MemoryStore) CreateRefreshToken(_ context.Context, rt *domain.RefreshToken) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.refreshTokens[rt.ID]; exists {
		return ErrConflict
	}
	s.refreshTokens[rt.ID] = *rt
	return nil
}

func (s *MemoryStore) GetRefreshToken(_ context.Context, id string) (*domain.RefreshToken, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rt, ok := s.refreshTokens[id]
	if !ok {
		return nil, ErrNotFound
	}
	return &rt, nil
}

func (s *MemoryStore) RevokeRefreshToken(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rt, ok := s.refreshTokens[id]
	if !ok {
		return ErrNotFound
	}
	rt.Revoked = true
	s.refreshTokens[id] = rt
	return nil
}

// RevokeRefreshTokenIfActive atomically (under the write lock) revokes the token
// only if it is currently active, mirroring the postgres conditional UPDATE.
// revoked=false (no error) means the row was already revoked — a lost rotation
// race or a replay of a rotated token.
func (s *MemoryStore) RevokeRefreshTokenIfActive(_ context.Context, id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rt, ok := s.refreshTokens[id]
	if !ok {
		return false, ErrNotFound
	}
	if rt.Revoked {
		return false, nil // compare-and-set lost: already revoked
	}
	rt.Revoked = true
	s.refreshTokens[id] = rt
	return true, nil
}

func (s *MemoryStore) RevokeAllUserRefreshTokens(_ context.Context, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, rt := range s.refreshTokens {
		if rt.UserID == userID && !rt.Revoked {
			rt.Revoked = true
			s.refreshTokens[id] = rt
		}
	}
	return nil
}

// DeleteExpiredRefreshTokens removes ONLY truly-expired rows (ExpiresAt before the
// cutoff), returning the number deleted. It deliberately KEEPS revoked-but-unexpired
// rows: a revoked row is the replay-detection tombstone a rotated token needs, so a
// late replay still sees rec.Revoked == true and triggers family revocation. The
// tombstone survives until its underlying JWT can no longer Verify (ExpiresAt passes).
func (s *MemoryStore) DeleteExpiredRefreshTokens(_ context.Context, before time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int64
	for id, rt := range s.refreshTokens {
		expired := !rt.ExpiresAt.IsZero() && rt.ExpiresAt.Before(before)
		if expired {
			delete(s.refreshTokens, id)
			n++
		}
	}
	return n, nil
}

// ---- Password reset tokens (single-use, time-limited, hashed at rest) ----

func (s *MemoryStore) CreatePasswordResetToken(_ context.Context, t *domain.PasswordResetToken) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.resetTokens[t.ID]; exists {
		return ErrConflict
	}
	for _, existing := range s.resetTokens {
		if existing.TokenHash == t.TokenHash {
			return ErrConflict
		}
	}
	s.resetTokens[t.ID] = *t
	return nil
}

// InvalidateUserPasswordResetTokens marks all of a user's currently-unused reset
// tokens used, so a newly issued token is the only live one for that user.
func (s *MemoryStore) InvalidateUserPasswordResetTokens(_ context.Context, userID string, usedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, t := range s.resetTokens {
		if t.UserID == userID && t.UsedAt.IsZero() {
			t.UsedAt = usedAt
			s.resetTokens[id] = t
		}
	}
	return nil
}

func (s *MemoryStore) GetPasswordResetTokenByHash(_ context.Context, tokenHash string) (*domain.PasswordResetToken, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, t := range s.resetTokens {
		if t.TokenHash == tokenHash {
			cp := t
			return &cp, nil
		}
	}
	return nil, ErrNotFound
}

// ConsumePasswordResetToken atomically marks the token used only if unused.
// consumed=false (no error) means it was already used (replay).
func (s *MemoryStore) ConsumePasswordResetToken(_ context.Context, id string, usedAt time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.resetTokens[id]
	if !ok {
		return false, ErrNotFound
	}
	if !t.UsedAt.IsZero() {
		return false, nil // already consumed
	}
	t.UsedAt = usedAt
	s.resetTokens[id] = t
	return true, nil
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

// AddUsageIfAbsent inserts the record only if no record with the same id already
// exists for the org, ATOMICALLY under the write lock. It returns inserted=false
// (no error) on a duplicate id, mirroring postgres INSERT ... ON CONFLICT (id) DO
// NOTHING — so a concurrent or repeated per-(org,hour) meter never double-counts.
func (s *MemoryStore) AddUsageIfAbsent(_ context.Context, u *domain.UsageRecord) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.usage[u.OrgID] {
		if r.ID == u.ID {
			return false, nil // already recorded -> idempotent skip
		}
	}
	s.usage[u.OrgID] = append(s.usage[u.OrgID], *u)
	return true, nil
}

func (s *MemoryStore) ListUsageByOrg(_ context.Context, orgID string) ([]domain.UsageRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.UsageRecord, len(s.usage[orgID]))
	copy(out, s.usage[orgID])
	return out, nil
}

func (s *MemoryStore) ListUsageByOrgSince(_ context.Context, orgID string, since time.Time) ([]domain.UsageRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.UsageRecord, 0, len(s.usage[orgID]))
	for _, r := range s.usage[orgID] {
		if !r.At.Before(since) {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *MemoryStore) GetSubscriptionByStripeID(_ context.Context, stripeSubID string) (*domain.Subscription, error) {
	if stripeSubID == "" {
		return nil, ErrNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, sub := range s.subscriptions {
		if sub.StripeSubscriptionID == stripeSubID {
			cp := sub
			return &cp, nil
		}
	}
	return nil, ErrNotFound
}

func (s *MemoryStore) GetSubscriptionByCustomerID(_ context.Context, customerID string) (*domain.Subscription, error) {
	if customerID == "" {
		return nil, ErrNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, sub := range s.subscriptions {
		if sub.StripeCustomerID == customerID {
			cp := sub
			return &cp, nil
		}
	}
	return nil, ErrNotFound
}

func (s *MemoryStore) EventProcessed(_ context.Context, eventID string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.processedEvts[eventID]
	return ok, nil
}

func (s *MemoryStore) MarkEventProcessed(_ context.Context, eventID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.processedEvts[eventID]; ok {
		return false, nil
	}
	s.processedEvts[eventID] = struct{}{}
	return true, nil
}

func (s *MemoryStore) GetMeterState(_ context.Context) (*domain.MeterState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.meterState == nil {
		return nil, ErrNotFound
	}
	cp := *s.meterState
	return &cp, nil
}

func (s *MemoryStore) SetMeterState(_ context.Context, st *domain.MeterState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *st
	s.meterState = &cp
	return nil
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

// envEntry is the in-memory app_env record: the at-rest value plus its secret flag.
type envEntry struct {
	value  string
	secret bool
}

func (s *MemoryStore) GetAppEnv(_ context.Context, appID string) (map[string]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string, len(s.appEnv[appID]))
	for k, v := range s.appEnv[appID] {
		out[k] = v.value
	}
	return out, nil
}

func (s *MemoryStore) ListAppEnv(_ context.Context, appID string) ([]domain.AppEnvEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.AppEnvEntry, 0, len(s.appEnv[appID]))
	for k, v := range s.appEnv[appID] {
		out = append(out, domain.AppEnvEntry{Key: k, Value: v.value, Secret: v.secret})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (s *MemoryStore) SetAppEnv(_ context.Context, appID, key, value string, secret bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.appEnv[appID] == nil {
		s.appEnv[appID] = make(map[string]envEntry)
	}
	s.appEnv[appID][key] = envEntry{value: value, secret: secret}
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

func (s *MemoryStore) CreateAuditEvent(_ context.Context, e *domain.AuditEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.auditEvents = append(s.auditEvents, *e)
	return nil
}

func (s *MemoryStore) ListAuditEvents(_ context.Context, f domain.AuditFilter) ([]domain.AuditEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	out := make([]domain.AuditEvent, 0, limit)
	// Most-recent-first: iterate the append-only slice in reverse.
	for i := len(s.auditEvents) - 1; i >= 0; i-- {
		if s.auditEvents[i].OrgID != f.OrgID {
			continue
		}
		out = append(out, s.auditEvents[i])
		if len(out) >= limit {
			break
		}
	}
	return out, nil
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

// GetVerifiedDomainByHost returns the single VERIFIED domain owning host
// (case-insensitive), regardless of app/org, so VerifyDomain can reject a second
// tenant claiming a host already verified by another. ErrNotFound when none.
func (s *MemoryStore) GetVerifiedDomainByHost(_ context.Context, host string) (*domain.Domain, error) {
	want := strings.ToLower(strings.TrimSpace(host))
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, d := range s.domains {
		if d.IsVerified() && strings.EqualFold(d.Domain, want) {
			cp := d
			return &cp, nil
		}
	}
	return nil, ErrNotFound
}

func (s *MemoryStore) UpdateDomain(_ context.Context, d *domain.Domain) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.domains[d.ID]; !ok {
		return ErrNotFound
	}
	s.domains[d.ID] = *d
	return nil
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

// ---- Pricing components ----

func (s *MemoryStore) ListPricingComponents(_ context.Context) ([]domain.PricingComponent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.PricingComponent, 0, len(s.pricing))
	for _, p := range s.pricing {
		out = append(out, p)
	}
	return out, nil
}

func (s *MemoryStore) GetPricingComponent(_ context.Context, key string) (*domain.PricingComponent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.pricing[key]
	if !ok {
		return nil, ErrNotFound
	}
	return &p, nil
}

func (s *MemoryStore) UpsertPricingComponent(_ context.Context, p *domain.PricingComponent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pricing[p.Key] = *p
	return nil
}

func (s *MemoryStore) DeletePricingComponent(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.pricing[key]; !ok {
		return ErrNotFound
	}
	delete(s.pricing, key)
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

// SumUsageByMetric aggregates total quantity per metric, mirroring the
// SQL GROUP BY done by the Postgres store.
func (s *MemoryStore) SumUsageByMetric(_ context.Context) (map[string]int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]int64)
	for _, recs := range s.usage {
		for _, u := range recs {
			out[u.Metric] += u.Quantity
		}
	}
	return out, nil
}
