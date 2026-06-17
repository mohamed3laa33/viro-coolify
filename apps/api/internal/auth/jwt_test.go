package auth

import (
	"testing"
	"time"
)

func TestIssueAndVerify(t *testing.T) {
	m := NewTokenManager("secret", 15*time.Minute, time.Hour)
	tok, err := m.Issue("user-1", AccessToken)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	claims, err := m.Verify(tok, AccessToken)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if claims.Subject != "user-1" {
		t.Fatalf("subject = %q", claims.Subject)
	}
}

func TestVerifyRejectsWrongType(t *testing.T) {
	m := NewTokenManager("secret", 15*time.Minute, time.Hour)
	tok, _ := m.Issue("user-1", RefreshToken)
	if _, err := m.Verify(tok, AccessToken); err == nil {
		t.Fatal("expected error verifying refresh token as access token")
	}
}

func TestVerifyRejectsWrongSecret(t *testing.T) {
	m1 := NewTokenManager("secret-a", time.Minute, time.Hour)
	m2 := NewTokenManager("secret-b", time.Minute, time.Hour)
	tok, _ := m1.Issue("user-1", AccessToken)
	if _, err := m2.Verify(tok, AccessToken); err == nil {
		t.Fatal("expected error verifying token signed with a different secret")
	}
}

func TestVerifyRejectsExpired(t *testing.T) {
	m := NewTokenManager("secret", time.Minute, time.Hour)
	// Issue a token that expired an hour ago by pinning the clock to the past.
	m.now = func() time.Time { return time.Now().Add(-2 * time.Hour) }
	tok, _ := m.Issue("user-1", AccessToken)
	m.now = time.Now
	if _, err := m.Verify(tok, AccessToken); err == nil {
		t.Fatal("expected error verifying expired token")
	}
}

func TestPasswordHashing(t *testing.T) {
	hash, err := HashPassword("hunter2-correct-horse")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !CheckPassword(hash, "hunter2-correct-horse") {
		t.Fatal("expected password to verify")
	}
	if CheckPassword(hash, "wrong") {
		t.Fatal("expected wrong password to fail")
	}
}
