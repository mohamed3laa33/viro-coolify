package identity

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/auth"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/notify"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/store"
)

// newServiceWithMailer builds a service backed by the in-memory store with the
// given recording mailer and Wave 6 options (base domain + short TTLs).
func newServiceWithMailer(t *testing.T, mailer notify.Mailer, opts ...Option) *Service {
	t.Helper()
	s := store.NewMemoryStore()
	tm := auth.NewTokenManager("test-secret", 15*time.Minute, time.Hour)
	base := []Option{
		WithMailer(mailer),
		WithBaseDomain("vortex.example.com"),
		WithRefreshTTL(time.Hour),
		WithInvitationTTL(7 * 24 * time.Hour),
		WithPasswordResetTTL(time.Hour),
	}
	return NewService(s, tm, nil, append(base, opts...)...)
}

// ---- 1. Email wiring ----

// TestSignupSendsWelcomeEmail asserts a welcome email is sent to the new user.
func TestSignupSendsWelcomeEmail(t *testing.T) {
	rec := notify.NewRecordingMailer()
	svc := newServiceWithMailer(t, rec)
	if _, err := svc.Signup(context.Background(), "new@example.com", "New", "supersecret"); err != nil {
		t.Fatalf("signup: %v", err)
	}
	msgs := rec.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 welcome email, got %d", len(msgs))
	}
	if msgs[0].To != "new@example.com" {
		t.Fatalf("welcome To = %q", msgs[0].To)
	}
	if !strings.Contains(msgs[0].Subject, "Welcome") {
		t.Fatalf("welcome subject = %q", msgs[0].Subject)
	}
}

// TestInviteSendsInvitationEmail asserts that Invite sends an invitation email to
// the invitee carrying the accept URL with the invitation token.
func TestInviteSendsInvitationEmail(t *testing.T) {
	rec := notify.NewRecordingMailer()
	svc := newServiceWithMailer(t, rec)
	ctx := context.Background()
	inviter, err := svc.Signup(ctx, "owner@example.com", "Owner", "supersecret")
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	orgs, _ := svc.ListOrganizations(ctx, inviter.User.ID)
	orgID := orgs[0].ID
	rec.Reset()

	inv, err := svc.Invite(ctx, inviter.User.ID, orgID, "", "invitee@example.com", domain.RoleMember)
	if err != nil {
		t.Fatalf("invite: %v", err)
	}
	last, ok := rec.Last()
	if !ok {
		t.Fatal("expected an invitation email to be sent")
	}
	if last.To != "invitee@example.com" {
		t.Fatalf("invitation To = %q, want invitee@example.com", last.To)
	}
	wantURL := "https://vortex.example.com/invite?token=" + inv.Token
	if !strings.Contains(last.HTMLBody, wantURL) || !strings.Contains(last.TextBody, wantURL) {
		t.Fatalf("invitation email missing accept URL %q\nHTML=%s\nTEXT=%s", wantURL, last.HTMLBody, last.TextBody)
	}
}

// TestInviteEmailFailureDoesNotBlock asserts a mailer error never fails Invite —
// the invitation is still persisted and returned.
func TestInviteEmailFailureDoesNotBlock(t *testing.T) {
	rec := notify.NewRecordingMailer()
	rec.Err = errors.New("smtp down")
	svc := newServiceWithMailer(t, rec)
	ctx := context.Background()
	inviter, _ := svc.Signup(ctx, "o2@example.com", "O", "supersecret")
	orgs, _ := svc.ListOrganizations(ctx, inviter.User.ID)

	inv, err := svc.Invite(ctx, inviter.User.ID, orgs[0].ID, "", "x@example.com", domain.RoleMember)
	if err != nil {
		t.Fatalf("invite should not fail on email error: %v", err)
	}
	if inv == nil || inv.Token == "" {
		t.Fatal("invitation should still be created")
	}
}

// ---- 2. Refresh family-revocation + atomic rotation ----

// TestRefreshReplayKillsFamily asserts replay of a rotated token revokes ALL the
// user's sessions.
func TestRefreshReplayKillsFamily(t *testing.T) {
	svc := newServiceWithMailer(t, notify.NewNoopMailer())
	ctx := context.Background()
	res, err := svc.Signup(ctx, "fam@example.com", "Fam", "supersecret")
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	// Rotate twice so there is a live, current token (rotated2).
	rotated, err := svc.Refresh(ctx, res.Refresh)
	if err != nil {
		t.Fatalf("refresh 1: %v", err)
	}
	rotated2, err := svc.Refresh(ctx, rotated.Refresh)
	if err != nil {
		t.Fatalf("refresh 2: %v", err)
	}
	// Replay the FIRST (already-rotated) token: rejected + kills the family.
	if _, err := svc.Refresh(ctx, res.Refresh); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("replay should be rejected, got %v", err)
	}
	// The live token is now dead too.
	if _, err := svc.Refresh(ctx, rotated2.Refresh); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("family should be revoked after replay, got %v", err)
	}
}

// TestConcurrentRotationMintsOneSession asserts that N concurrent refreshes of the
// SAME token mint exactly one new session (atomic compare-and-set rotation).
func TestConcurrentRotationMintsOneSession(t *testing.T) {
	svc := newServiceWithMailer(t, notify.NewNoopMailer())
	ctx := context.Background()
	res, err := svc.Signup(ctx, "race@example.com", "Race", "supersecret")
	if err != nil {
		t.Fatalf("signup: %v", err)
	}

	const n = 16
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			if _, err := svc.Refresh(ctx, res.Refresh); err == nil {
				mu.Lock()
				successes++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if successes != 1 {
		t.Fatalf("expected exactly 1 successful rotation, got %d", successes)
	}
}

// TestRefreshRejectsExpiredStoredToken asserts an expired stored refresh token is
// rejected even if the JWT itself is otherwise valid.
func TestRefreshRejectsExpiredStoredToken(t *testing.T) {
	s := store.NewMemoryStore()
	tm := auth.NewTokenManager("test-secret", 15*time.Minute, time.Hour)
	now := time.Now()
	clock := now
	svc := NewService(s, tm, nil,
		WithMailer(notify.NewNoopMailer()),
		WithRefreshTTL(time.Hour),
	)
	svc.now = func() time.Time { return clock }

	res, err := svc.Signup(context.Background(), "exp@example.com", "Exp", "supersecret")
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	// Advance the clock past the stored refresh expiry.
	clock = now.Add(2 * time.Hour)
	if _, err := svc.Refresh(context.Background(), res.Refresh); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected expired stored token rejected, got %v", err)
	}
}

// ---- 4. Refresh cleanup ----

// TestCleanupExpiredRefreshTokens asserts the cleanup deletes expired+revoked rows.
func TestCleanupExpiredRefreshTokens(t *testing.T) {
	s := store.NewMemoryStore()
	tm := auth.NewTokenManager("test-secret", 15*time.Minute, time.Hour)
	now := time.Now()
	clock := now
	svc := NewService(s, tm, nil, WithMailer(notify.NewNoopMailer()), WithRefreshTTL(time.Hour))
	svc.now = func() time.Time { return clock }
	ctx := context.Background()

	if _, err := svc.Signup(ctx, "gc@example.com", "GC", "supersecret"); err != nil {
		t.Fatalf("signup: %v", err)
	}
	// Before expiry, nothing is collected.
	if n, err := svc.CleanupExpiredRefreshTokens(ctx); err != nil || n != 0 {
		t.Fatalf("cleanup before expiry n=%d err=%v", n, err)
	}
	// After expiry, the stored token row is collected.
	clock = now.Add(2 * time.Hour)
	if n, err := svc.CleanupExpiredRefreshTokens(ctx); err != nil || n != 1 {
		t.Fatalf("cleanup after expiry n=%d err=%v, want 1", n, err)
	}
}

// TestRefreshReplaySurvivesCleanupAndKillsFamily asserts the replay-detection
// tombstone survives a cleanup tick: after rotating a token and running the cleanup
// GC, replaying the OLD token must STILL trip family revocation (all other sessions
// die). This guards the MAJOR fix — DeleteExpiredRefreshTokens must NOT purge a
// revoked-but-unexpired row, or the theft signal is swallowed.
func TestRefreshReplaySurvivesCleanupAndKillsFamily(t *testing.T) {
	svc := newServiceWithMailer(t, notify.NewNoopMailer())
	ctx := context.Background()
	res, err := svc.Signup(ctx, "replay@example.com", "Replay", "supersecret")
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	// Rotate once: res.Refresh becomes a revoked tombstone, rotated.Refresh is live.
	rotated, err := svc.Refresh(ctx, res.Refresh)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	// Run the cleanup GC (as the background ticker does). The revoked tombstone for
	// res.Refresh is unexpired, so it MUST survive — only truly-expired rows go.
	if _, err := svc.CleanupExpiredRefreshTokens(ctx); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	// Replay the old (rotated-away) token AFTER cleanup: still rejected AND must kill
	// the family via the surviving tombstone.
	if _, err := svc.Refresh(ctx, res.Refresh); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("post-cleanup replay should be rejected, got %v", err)
	}
	// The previously-live rotated token is now dead too: the family was revoked.
	if _, err := svc.Refresh(ctx, rotated.Refresh); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("family must be revoked after post-cleanup replay, got %v", err)
	}
}

// ---- 6. Password reset ----

// TestForgotPasswordEnumerationSafe asserts ForgotPassword returns nil for both
// known and unknown emails, and only sends an email for a known user.
func TestForgotPasswordEnumerationSafe(t *testing.T) {
	rec := notify.NewRecordingMailer()
	svc := newServiceWithMailer(t, rec)
	ctx := context.Background()
	if _, err := svc.Signup(ctx, "real@example.com", "R", "supersecret"); err != nil {
		t.Fatalf("signup: %v", err)
	}
	rec.Reset()

	// Unknown email: nil error, no email sent.
	if err := svc.ForgotPassword(ctx, "ghost@example.com"); err != nil {
		t.Fatalf("forgot unknown: %v", err)
	}
	if rec.Count() != 0 {
		t.Fatalf("no email should be sent for unknown user, got %d", rec.Count())
	}
	// Known email: nil error, exactly one email sent.
	if err := svc.ForgotPassword(ctx, "real@example.com"); err != nil {
		t.Fatalf("forgot known: %v", err)
	}
	if rec.Count() != 1 {
		t.Fatalf("expected 1 reset email for known user, got %d", rec.Count())
	}
	if last, _ := rec.Last(); last.To != "real@example.com" {
		t.Fatalf("reset email To = %q", last.To)
	}
}

// resetTokenFromEmail extracts the reset token from the link in a reset email.
func resetTokenFromEmail(t *testing.T, body string) string {
	t.Helper()
	const marker = "token="
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("no reset token in email body: %s", body)
	}
	rest := body[i+len(marker):]
	// Token ends at the first whitespace/newline or end of string.
	end := strings.IndexAny(rest, " \r\n<\"")
	if end < 0 {
		end = len(rest)
	}
	return rest[:end]
}

// TestResetPasswordRotatesAndKillsSessions asserts a valid reset sets the new
// password and revokes all the user's refresh tokens.
func TestResetPasswordRotatesAndKillsSessions(t *testing.T) {
	rec := notify.NewRecordingMailer()
	svc := newServiceWithMailer(t, rec)
	ctx := context.Background()
	res, err := svc.Signup(ctx, "pw@example.com", "PW", "supersecret")
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	rec.Reset()
	if err := svc.ForgotPassword(ctx, "pw@example.com"); err != nil {
		t.Fatalf("forgot: %v", err)
	}
	last, _ := rec.Last()
	token := resetTokenFromEmail(t, last.TextBody)

	if err := svc.ResetPassword(ctx, token, "brand-new-pass"); err != nil {
		t.Fatalf("reset: %v", err)
	}
	// New password logs in.
	if _, err := svc.Login(ctx, "pw@example.com", "brand-new-pass"); err != nil {
		t.Fatalf("login with new password: %v", err)
	}
	// Old password no longer works.
	if _, err := svc.Login(ctx, "pw@example.com", "supersecret"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old password should be rejected, got %v", err)
	}
	// The pre-reset refresh token is revoked.
	if _, err := svc.Refresh(ctx, res.Refresh); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("pre-reset session should be killed, got %v", err)
	}
}

// TestResetPasswordRejectsUsedToken asserts a token cannot be used twice.
func TestResetPasswordRejectsUsedToken(t *testing.T) {
	rec := notify.NewRecordingMailer()
	svc := newServiceWithMailer(t, rec)
	ctx := context.Background()
	if _, err := svc.Signup(ctx, "used@example.com", "U", "supersecret"); err != nil {
		t.Fatalf("signup: %v", err)
	}
	rec.Reset()
	if err := svc.ForgotPassword(ctx, "used@example.com"); err != nil {
		t.Fatalf("forgot: %v", err)
	}
	last, _ := rec.Last()
	token := resetTokenFromEmail(t, last.TextBody)

	if err := svc.ResetPassword(ctx, token, "first-new-pass"); err != nil {
		t.Fatalf("first reset: %v", err)
	}
	if err := svc.ResetPassword(ctx, token, "second-new-pass"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("reused reset token should be rejected, got %v", err)
	}
}

// TestForgotPasswordInvalidatesPriorTokens asserts a second forgot-password request
// invalidates the FIRST reset token: only the most recent link stays live, so a
// stale/leaked earlier link can no longer be redeemed.
func TestForgotPasswordInvalidatesPriorTokens(t *testing.T) {
	rec := notify.NewRecordingMailer()
	svc := newServiceWithMailer(t, rec)
	ctx := context.Background()
	if _, err := svc.Signup(ctx, "two@example.com", "Two", "supersecret"); err != nil {
		t.Fatalf("signup: %v", err)
	}
	rec.Reset()

	// First forgot -> first token.
	if err := svc.ForgotPassword(ctx, "two@example.com"); err != nil {
		t.Fatalf("forgot 1: %v", err)
	}
	first, _ := rec.Last()
	firstToken := resetTokenFromEmail(t, first.TextBody)

	// Second forgot -> second token (and must invalidate the first).
	if err := svc.ForgotPassword(ctx, "two@example.com"); err != nil {
		t.Fatalf("forgot 2: %v", err)
	}
	second, _ := rec.Last()
	secondToken := resetTokenFromEmail(t, second.TextBody)
	if firstToken == secondToken {
		t.Fatal("second forgot should mint a distinct token")
	}

	// The FIRST token is now dead.
	if err := svc.ResetPassword(ctx, firstToken, "via-first-pass"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("first token should be invalidated by the second forgot, got %v", err)
	}
	// The SECOND token still works.
	if err := svc.ResetPassword(ctx, secondToken, "via-second-pass"); err != nil {
		t.Fatalf("second (current) token should still reset: %v", err)
	}
	if _, err := svc.Login(ctx, "two@example.com", "via-second-pass"); err != nil {
		t.Fatalf("login with reset password: %v", err)
	}
}

// TestResetPasswordRejectsExpiredToken asserts an expired reset token is rejected.
func TestResetPasswordRejectsExpiredToken(t *testing.T) {
	rec := notify.NewRecordingMailer()
	s := store.NewMemoryStore()
	tm := auth.NewTokenManager("test-secret", 15*time.Minute, time.Hour)
	now := time.Now()
	clock := now
	svc := NewService(s, tm, nil,
		WithMailer(rec),
		WithBaseDomain("vortex.example.com"),
		WithPasswordResetTTL(time.Hour),
	)
	svc.now = func() time.Time { return clock }
	ctx := context.Background()
	if _, err := svc.Signup(ctx, "expreset@example.com", "E", "supersecret"); err != nil {
		t.Fatalf("signup: %v", err)
	}
	rec.Reset()
	if err := svc.ForgotPassword(ctx, "expreset@example.com"); err != nil {
		t.Fatalf("forgot: %v", err)
	}
	last, _ := rec.Last()
	token := resetTokenFromEmail(t, last.TextBody)

	clock = now.Add(2 * time.Hour) // past the reset TTL
	if err := svc.ResetPassword(ctx, token, "whatever-pass"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expired reset token should be rejected, got %v", err)
	}
}

// ---- 7. Invitation expiry ----

// TestAcceptInvitationRejectsExpired asserts an expired invitation cannot be
// accepted.
func TestAcceptInvitationRejectsExpired(t *testing.T) {
	s := store.NewMemoryStore()
	tm := auth.NewTokenManager("test-secret", 15*time.Minute, time.Hour)
	now := time.Now()
	clock := now
	svc := NewService(s, tm, nil,
		WithMailer(notify.NewNoopMailer()),
		WithInvitationTTL(7*24*time.Hour),
	)
	svc.now = func() time.Time { return clock }
	ctx := context.Background()

	owner, err := svc.Signup(ctx, "inviter@example.com", "Inviter", "supersecret")
	if err != nil {
		t.Fatalf("signup owner: %v", err)
	}
	orgs, _ := svc.ListOrganizations(ctx, owner.User.ID)
	invitee, err := svc.Signup(ctx, "guest@example.com", "Guest", "supersecret")
	if err != nil {
		t.Fatalf("signup guest: %v", err)
	}
	inv, err := svc.Invite(ctx, owner.User.ID, orgs[0].ID, "", "guest@example.com", domain.RoleMember)
	if err != nil {
		t.Fatalf("invite: %v", err)
	}
	// Advance past the invitation TTL.
	clock = now.Add(8 * 24 * time.Hour)
	if _, err := svc.AcceptInvitation(ctx, invitee.User.ID, invitee.User.Email, inv.Token); !errors.Is(err, ErrInvitationInvalid) {
		t.Fatalf("expired invitation should be rejected, got %v", err)
	}
}

// TestAcceptInvitationWithinTTL asserts a non-expired invitation is accepted.
func TestAcceptInvitationWithinTTL(t *testing.T) {
	svc := newServiceWithMailer(t, notify.NewNoopMailer())
	ctx := context.Background()
	owner, _ := svc.Signup(ctx, "inv2@example.com", "Inviter", "supersecret")
	orgs, _ := svc.ListOrganizations(ctx, owner.User.ID)
	invitee, _ := svc.Signup(ctx, "guest2@example.com", "Guest", "supersecret")
	inv, err := svc.Invite(ctx, owner.User.ID, orgs[0].ID, "", "guest2@example.com", domain.RoleMember)
	if err != nil {
		t.Fatalf("invite: %v", err)
	}
	if _, err := svc.AcceptInvitation(ctx, invitee.User.ID, invitee.User.Email, inv.Token); err != nil {
		t.Fatalf("accept within TTL: %v", err)
	}
}
