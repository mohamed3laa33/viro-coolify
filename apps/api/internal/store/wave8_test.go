package store

import (
	"context"
	"testing"
	"time"

	"github.com/pashagolub/pgxmock/v4"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
)

// TestCreateDomain_Postgres asserts a pending custom domain persists its
// verification columns (status/token/verified_at) via CreateDomain.
func TestCreateDomain_Postgres(t *testing.T) {
	s, mock := newMockStore(t)
	defer mock.Close()

	now := time.Now()
	d := &domain.Domain{
		ID: "d1", OrgID: "org-1", AppID: "app-1", Domain: "shop.acme.io",
		Verified: false, Status: domain.DomainPending, VerificationToken: "tok123",
		CreatedAt: now,
	}
	mock.ExpectExec("INSERT INTO domains").
		WithArgs("d1", "org-1", "app-1", "shop.acme.io", false, "pending", "tok123", nilTime(), now).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	if err := s.CreateDomain(context.Background(), d); err != nil {
		t.Fatalf("CreateDomain: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestUpdateDomain_Postgres asserts a verified transition writes the new status,
// the mirrored boolean, and verified_at.
func TestUpdateDomain_Postgres(t *testing.T) {
	s, mock := newMockStore(t)
	defer mock.Close()

	at := time.Now()
	d := &domain.Domain{
		ID: "d1", Domain: "shop.acme.io", Verified: true,
		Status: domain.DomainVerified, VerificationToken: "tok123", VerifiedAt: at,
	}
	mock.ExpectExec("UPDATE domains SET").
		WithArgs("d1", "shop.acme.io", true, "verified", "tok123", &at).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	if err := s.UpdateDomain(context.Background(), d); err != nil {
		t.Fatalf("UpdateDomain: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestGetDomain_Postgres asserts the verification columns scan back, including a
// NULL verified_at for a pending row.
func TestGetDomain_Postgres(t *testing.T) {
	s, mock := newMockStore(t)
	defer mock.Close()

	now := time.Now()
	mock.ExpectQuery("SELECT id, org_id, app_id, domain, verified, status, verification_token, verified_at, created_at FROM domains").
		WithArgs("d1").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "org_id", "app_id", "domain", "verified", "status", "verification_token", "verified_at", "created_at",
		}).AddRow("d1", "org-1", "app-1", "shop.acme.io", false, "pending", "tok123", nilTime(), now))

	got, err := s.GetDomain(context.Background(), "d1")
	if err != nil {
		t.Fatalf("GetDomain: %v", err)
	}
	if got.Status != domain.DomainPending || got.VerificationToken != "tok123" || !got.VerifiedAt.IsZero() {
		t.Fatalf("unexpected domain: %+v", got)
	}
}

// nilTime returns a typed nil *time.Time for NULL verified_at expectations.
func nilTime() *time.Time { return nil }

// TestGetVerifiedDomainByHost_Memory asserts the global hostname-ownership lookup:
// it returns the single VERIFIED row (case-insensitively) and ErrNotFound when no
// verified row owns the host, ignoring pending/failed rows referencing the host.
func TestGetVerifiedDomainByHost_Memory(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	now := time.Now()

	// A pending row for the host: must NOT be returned as the owner.
	if err := s.CreateDomain(ctx, &domain.Domain{
		ID: "d-pending", OrgID: "org-2", AppID: "app-2", Domain: "Example.COM",
		Status: domain.DomainPending, CreatedAt: now,
	}); err != nil {
		t.Fatalf("create pending: %v", err)
	}
	// No verified row yet -> ErrNotFound (even though a pending row references it).
	if _, err := s.GetVerifiedDomainByHost(ctx, "example.com"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound before any verified row, got %v", err)
	}

	// The verified owner (stored with different casing to exercise case-insensitive match).
	if err := s.CreateDomain(ctx, &domain.Domain{
		ID: "d-verified", OrgID: "org-1", AppID: "app-1", Domain: "example.com",
		Verified: true, Status: domain.DomainVerified, VerifiedAt: now, CreatedAt: now,
	}); err != nil {
		t.Fatalf("create verified: %v", err)
	}

	got, err := s.GetVerifiedDomainByHost(ctx, "EXAMPLE.com")
	if err != nil {
		t.Fatalf("GetVerifiedDomainByHost: %v", err)
	}
	if got.ID != "d-verified" {
		t.Fatalf("expected the verified owner d-verified, got %+v", got)
	}

	// A host nobody verified -> ErrNotFound.
	if _, err := s.GetVerifiedDomainByHost(ctx, "other.example.org"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound for unowned host, got %v", err)
	}
}
