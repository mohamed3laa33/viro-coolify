package httpx

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/config"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/kube"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/notify"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/store"
)

// newTestServerWithMailer builds a Server wired with the given recording mailer so
// tests can assert on sent email.
func newTestServerWithMailer(t *testing.T, mailer notify.Mailer) *Server {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return NewServer(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)),
		store.NewMemoryStore(), WithBackend(kube.NewFakeBackend()), WithMailer(mailer))
}

// doJSONOrigin performs a JSON request with an explicit Origin header (browser).
func doJSONOrigin(t *testing.T, s *Server, method, path, body, origin string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	return rec
}

// ---- 5. Browser body-token omission ----

// TestBrowserSignupOmitsBodyTokens asserts a BROWSER signup (Origin present) gets
// Set-Cookie but NO tokens in the JSON body, while a CLI signup (no Origin) gets
// body tokens.
func TestBrowserSignupOmitsBodyTokens(t *testing.T) {
	s := newTestServer(t, "http://unused")

	// Browser: Origin header present.
	rec := doJSONOrigin(t, s, http.MethodPost, "/v1/auth/signup",
		`{"email":"browser@example.com","name":"B","password":"supersecret"}`, "http://localhost:3000")
	if rec.Code != http.StatusCreated {
		t.Fatalf("signup = %d, body=%s", rec.Code, rec.Body.String())
	}
	if cookieByName(rec, accessCookieName) == nil || cookieByName(rec, refreshCookieName) == nil {
		t.Fatal("browser signup must set auth cookies")
	}
	var a authResponse
	_ = json.NewDecoder(rec.Body).Decode(&a)
	if a.AccessToken != "" || a.RefreshToken != "" {
		t.Fatalf("browser body must omit tokens, got access=%q refresh=%q", a.AccessToken, a.RefreshToken)
	}

	// CLI: no Origin header -> body carries tokens.
	rec = doJSON(t, s, http.MethodPost, "/v1/auth/signup",
		`{"email":"cli@example.com","name":"C","password":"supersecret"}`, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("cli signup = %d", rec.Code)
	}
	var b authResponse
	_ = json.NewDecoder(rec.Body).Decode(&b)
	if b.AccessToken == "" || b.RefreshToken == "" {
		t.Fatal("CLI body must carry tokens")
	}
}

// TestBrowserLoginAndRefreshOmitBodyTokens asserts login and refresh also omit
// body tokens for browsers but set cookies.
func TestBrowserLoginAndRefreshOmitBodyTokens(t *testing.T) {
	s := newTestServer(t, "http://unused")
	// Seed a user via CLI signup so we have a refresh token for the refresh test.
	cli := doJSON(t, s, http.MethodPost, "/v1/auth/signup",
		`{"email":"blr@example.com","name":"B","password":"supersecret"}`, "")
	var seed authResponse
	_ = json.NewDecoder(cli.Body).Decode(&seed)

	// Browser login.
	rec := doJSONOrigin(t, s, http.MethodPost, "/v1/auth/login",
		`{"email":"blr@example.com","password":"supersecret"}`, "http://localhost:3000")
	if rec.Code != http.StatusOK {
		t.Fatalf("login = %d", rec.Code)
	}
	if cookieByName(rec, accessCookieName) == nil {
		t.Fatal("browser login must set access cookie")
	}
	var a authResponse
	_ = json.NewDecoder(rec.Body).Decode(&a)
	if a.AccessToken != "" || a.RefreshToken != "" {
		t.Fatal("browser login body must omit tokens")
	}

	// Browser refresh (token via body, but Origin present -> omit response tokens).
	rec = doJSONOrigin(t, s, http.MethodPost, "/v1/auth/refresh",
		`{"refreshToken":"`+seed.RefreshToken+`"}`, "http://localhost:3000")
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh = %d, body=%s", rec.Code, rec.Body.String())
	}
	if cookieByName(rec, refreshCookieName) == nil {
		t.Fatal("browser refresh must set a new refresh cookie")
	}
	var rr authResponse
	_ = json.NewDecoder(rec.Body).Decode(&rr)
	if rr.AccessToken != "" || rr.RefreshToken != "" {
		t.Fatal("browser refresh body must omit tokens")
	}
}

// ---- 1. Invitation email end-to-end through the HTTP layer ----

// TestCreateInvitationSendsEmail asserts that creating an invitation via the API
// sends an invitation email to the invitee with the accept URL.
func TestCreateInvitationSendsEmail(t *testing.T) {
	rec := notify.NewRecordingMailer()
	s := newTestServerWithMailer(t, rec)

	// Sign up an owner (CLI flow so we get a usable bearer token).
	su := doJSON(t, s, http.MethodPost, "/v1/auth/signup",
		`{"email":"inviter@example.com","name":"Inviter","password":"supersecret"}`, "")
	if su.Code != http.StatusCreated {
		t.Fatalf("signup = %d", su.Code)
	}
	var a authResponse
	_ = json.NewDecoder(su.Body).Decode(&a)

	// Find the owner's org.
	orgsRec := doJSON(t, s, http.MethodGet, "/v1/orgs", "", a.AccessToken)
	var orgsResp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = json.NewDecoder(orgsRec.Body).Decode(&orgsResp)
	if len(orgsResp.Data) == 0 {
		t.Fatalf("no orgs for owner: %s", orgsRec.Body.String())
	}
	orgID := orgsResp.Data[0].ID
	rec.Reset()

	// Create the invitation.
	inviteRec := doJSON(t, s, http.MethodPost, "/v1/orgs/"+orgID+"/invitations",
		`{"email":"invitee@example.com","role":"member"}`, a.AccessToken)
	if inviteRec.Code != http.StatusCreated {
		t.Fatalf("invite = %d, body=%s", inviteRec.Code, inviteRec.Body.String())
	}
	var inv struct {
		Token string `json:"token"`
	}
	_ = json.NewDecoder(inviteRec.Body).Decode(&inv)

	last, ok := rec.Last()
	if !ok {
		t.Fatal("expected an invitation email")
	}
	if last.To != "invitee@example.com" {
		t.Fatalf("invitation To = %q", last.To)
	}
	if !strings.Contains(last.TextBody, inv.Token) {
		t.Fatalf("invitation email missing token %q: %s", inv.Token, last.TextBody)
	}
}

// ---- 6. Password reset HTTP ----

// TestForgotPasswordIsEnumerationSafeHTTP asserts the forgot endpoint always 204s
// regardless of whether the email exists.
func TestForgotPasswordIsEnumerationSafeHTTP(t *testing.T) {
	rec := notify.NewRecordingMailer()
	s := newTestServerWithMailer(t, rec)
	_ = doJSON(t, s, http.MethodPost, "/v1/auth/signup",
		`{"email":"known@example.com","name":"K","password":"supersecret"}`, "")
	rec.Reset()

	// Unknown email -> 204, no email.
	r := doJSON(t, s, http.MethodPost, "/v1/auth/password/forgot", `{"email":"nobody@example.com"}`, "")
	if r.Code != http.StatusNoContent {
		t.Fatalf("forgot unknown = %d, want 204", r.Code)
	}
	if rec.Count() != 0 {
		t.Fatalf("unknown email should send nothing, got %d", rec.Count())
	}
	// Known email -> 204, one email.
	r = doJSON(t, s, http.MethodPost, "/v1/auth/password/forgot", `{"email":"known@example.com"}`, "")
	if r.Code != http.StatusNoContent {
		t.Fatalf("forgot known = %d, want 204", r.Code)
	}
	if rec.Count() != 1 {
		t.Fatalf("known email should send one reset email, got %d", rec.Count())
	}
}

// TestResetPasswordHTTP asserts the reset endpoint sets the new password and that a
// used/expired token is rejected with 401.
func TestResetPasswordHTTP(t *testing.T) {
	rec := notify.NewRecordingMailer()
	s := newTestServerWithMailer(t, rec)
	_ = doJSON(t, s, http.MethodPost, "/v1/auth/signup",
		`{"email":"resetme@example.com","name":"R","password":"supersecret"}`, "")
	rec.Reset()
	if r := doJSON(t, s, http.MethodPost, "/v1/auth/password/forgot", `{"email":"resetme@example.com"}`, ""); r.Code != http.StatusNoContent {
		t.Fatalf("forgot = %d", r.Code)
	}
	last, _ := rec.Last()
	token := resetTokenFromBody(t, last.TextBody)

	// Reset succeeds (204).
	if r := doJSON(t, s, http.MethodPost, "/v1/auth/password/reset",
		`{"token":"`+token+`","password":"a-new-password"}`, ""); r.Code != http.StatusNoContent {
		t.Fatalf("reset = %d", r.Code)
	}
	// New password logs in.
	if r := doJSON(t, s, http.MethodPost, "/v1/auth/login",
		`{"email":"resetme@example.com","password":"a-new-password"}`, ""); r.Code != http.StatusOK {
		t.Fatalf("login with new password = %d", r.Code)
	}
	// Reusing the token is rejected (401).
	if r := doJSON(t, s, http.MethodPost, "/v1/auth/password/reset",
		`{"token":"`+token+`","password":"another-pass"}`, ""); r.Code != http.StatusUnauthorized {
		t.Fatalf("reused reset token = %d, want 401", r.Code)
	}
}

// resetTokenFromBody extracts the reset token from a reset email body.
func resetTokenFromBody(t *testing.T, body string) string {
	t.Helper()
	const marker = "token="
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("no token in body: %s", body)
	}
	rest := body[i+len(marker):]
	end := strings.IndexAny(rest, " \r\n<\"")
	if end < 0 {
		end = len(rest)
	}
	return rest[:end]
}
