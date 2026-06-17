package store

import (
	"context"
	"strings"
	"sync"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
)

// MemoryStore is a thread-safe, in-memory Store for local development and tests.
type MemoryStore struct {
	mu            sync.RWMutex
	users         map[string]domain.User // by id
	usersByEmail  map[string]string      // email -> id
	organizations map[string]domain.Organization
	memberships   map[string]domain.Membership // key: orgID + "\x00" + userID
	apps          map[string]domain.App        // by id
	databases     map[string]domain.Database   // by id
	subscriptions map[string]domain.Subscription // by orgID
	usage         map[string][]domain.UsageRecord // by orgID
}

// NewMemoryStore returns an empty in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		users:         make(map[string]domain.User),
		usersByEmail:  make(map[string]string),
		organizations: make(map[string]domain.Organization),
		memberships:   make(map[string]domain.Membership),
		apps:          make(map[string]domain.App),
		databases:     make(map[string]domain.Database),
		subscriptions: make(map[string]domain.Subscription),
		usage:         make(map[string][]domain.UsageRecord),
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
