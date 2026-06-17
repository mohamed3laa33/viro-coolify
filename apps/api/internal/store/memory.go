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
}

// NewMemoryStore returns an empty in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		users:         make(map[string]domain.User),
		usersByEmail:  make(map[string]string),
		organizations: make(map[string]domain.Organization),
		memberships:   make(map[string]domain.Membership),
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
