package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pashagolub/pgxmock/v4"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
)

// ---- Memory: refresh-token compare-and-set ----

func TestMemoryRevokeRefreshTokenIfActive(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	if err := s.CreateRefreshToken(ctx, &domain.RefreshToken{ID: "jti", UserID: "u", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("create: %v", err)
	}
	// First CAS wins.
	ok, err := s.RevokeRefreshTokenIfActive(ctx, "jti")
	if err != nil || !ok {
		t.Fatalf("first CAS ok=%v err=%v, want true", ok, err)
	}
	// Second CAS loses (already revoked) — no error, ok=false.
	ok, err = s.RevokeRefreshTokenIfActive(ctx, "jti")
	if err != nil || ok {
		t.Fatalf("second CAS ok=%v err=%v, want false", ok, err)
	}
	// Missing row -> ErrNotFound.
	if _, err := s.RevokeRefreshTokenIfActive(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing CAS err=%v, want ErrNotFound", err)
	}
}

func TestMemoryDeleteExpiredRefreshTokens(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	now := time.Now()
	// active (not expired), expired, and revoked-but-unexpired rows.
	_ = s.CreateRefreshToken(ctx, &domain.RefreshToken{ID: "active", UserID: "u", CreatedAt: now, ExpiresAt: now.Add(time.Hour)})
	_ = s.CreateRefreshToken(ctx, &domain.RefreshToken{ID: "expired", UserID: "u", CreatedAt: now, ExpiresAt: now.Add(-time.Hour)})
	_ = s.CreateRefreshToken(ctx, &domain.RefreshToken{ID: "revoked", UserID: "u", CreatedAt: now, ExpiresAt: now.Add(time.Hour), Revoked: true})

	n, err := s.DeleteExpiredRefreshTokens(ctx, now)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if n != 1 {
		t.Fatalf("deleted %d, want 1 (only the truly-expired row)", n)
	}
	if _, err := s.GetRefreshToken(ctx, "active"); err != nil {
		t.Fatalf("active token should survive: %v", err)
	}
	// The revoked-but-unexpired tombstone MUST survive so a late replay still trips
	// family revocation in Refresh.
	if _, err := s.GetRefreshToken(ctx, "revoked"); err != nil {
		t.Fatalf("revoked-but-unexpired tombstone must survive cleanup: %v", err)
	}
	if _, err := s.GetRefreshToken(ctx, "expired"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired token should be gone, got %v", err)
	}
}

// ---- Memory: password reset single-use ----

func TestMemoryConsumePasswordResetToken(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	rec := &domain.PasswordResetToken{ID: "r1", UserID: "u", TokenHash: "hash", ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now()}
	if err := s.CreatePasswordResetToken(ctx, rec); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := s.GetPasswordResetTokenByHash(ctx, "hash")
	if err != nil || got.ID != "r1" {
		t.Fatalf("get by hash: %+v err=%v", got, err)
	}
	// First consume wins.
	ok, err := s.ConsumePasswordResetToken(ctx, "r1", time.Now())
	if err != nil || !ok {
		t.Fatalf("first consume ok=%v err=%v, want true", ok, err)
	}
	// Second consume loses.
	ok, err = s.ConsumePasswordResetToken(ctx, "r1", time.Now())
	if err != nil || ok {
		t.Fatalf("second consume ok=%v err=%v, want false", ok, err)
	}
}

// ---- Memory: WithTx commits applied writes ----

func TestMemoryWithTxCommits(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	err := s.WithTx(ctx, func(tx Store) error {
		return tx.CreateOrganization(ctx, &domain.Organization{ID: "o1", Name: "Org", Slug: "org", CreatedAt: time.Now()})
	})
	if err != nil {
		t.Fatalf("withtx: %v", err)
	}
	if _, err := s.GetOrganization(ctx, "o1"); err != nil {
		t.Fatalf("org should exist after commit: %v", err)
	}
}

// TestMemoryWithTxErrorPropagates asserts a fn error is returned from WithTx (the
// memory store cannot roll back, which is documented behavior).
func TestMemoryWithTxErrorPropagates(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	sentinel := errors.New("boom")
	if err := s.WithTx(ctx, func(_ Store) error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatalf("withtx err = %v, want sentinel", err)
	}
}

// ---- Postgres: WithTx commit / rollback ----

func TestPostgresWithTxCommit(t *testing.T) {
	s, mock := newMockStore(t)
	defer mock.Close()

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO organizations").
		WithArgs("o1", "Org", "org", "", int64(0), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	err := s.WithTx(context.Background(), func(tx Store) error {
		return tx.CreateOrganization(context.Background(), &domain.Organization{ID: "o1", Name: "Org", Slug: "org", CreatedAt: time.Now()})
	})
	if err != nil {
		t.Fatalf("withtx commit: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestPostgresWithTxRollback asserts that when fn fails mid-sequence the tx is
// rolled back and NOT committed — the core guarantee that a mid-Signup failure
// leaves no partial user/org.
func TestPostgresWithTxRollback(t *testing.T) {
	s, mock := newMockStore(t)
	defer mock.Close()

	failure := errors.New("membership write failed")

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO users").
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO memberships").
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnError(failure)
	mock.ExpectRollback()

	err := s.WithTx(context.Background(), func(tx Store) error {
		if e := tx.CreateUser(context.Background(), &domain.User{ID: "u1", Email: "a@b.com", Name: "A", CreatedAt: time.Now()}); e != nil {
			return e
		}
		return tx.AddMembership(context.Background(), domain.Membership{OrgID: "o1", UserID: "u1", Role: domain.RoleOwner})
	})
	if !errors.Is(err, failure) {
		t.Fatalf("withtx err = %v, want failure", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations (rollback must have happened): %v", err)
	}
}

// TestPostgresConsumePasswordResetToken_CAS asserts the conditional UPDATE +
// existence check: 1 row -> consumed, 0 rows but exists -> already used.
func TestPostgresConsumePasswordResetToken_CAS(t *testing.T) {
	s, mock := newMockStore(t)
	defer mock.Close()

	// First call consumes (1 row affected).
	mock.ExpectExec("UPDATE password_reset_tokens SET used_at").
		WithArgs("r1", pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	ok, err := s.ConsumePasswordResetToken(context.Background(), "r1", time.Now())
	if err != nil || !ok {
		t.Fatalf("first consume ok=%v err=%v", ok, err)
	}

	// Second call: 0 rows, but row exists -> already used (ok=false, no error).
	mock.ExpectExec("UPDATE password_reset_tokens SET used_at").
		WithArgs("r1", pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	mock.ExpectQuery("SELECT EXISTS").
		WithArgs("r1").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
	ok, err = s.ConsumePasswordResetToken(context.Background(), "r1", time.Now())
	if err != nil || ok {
		t.Fatalf("second consume ok=%v err=%v, want false/nil", ok, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}
