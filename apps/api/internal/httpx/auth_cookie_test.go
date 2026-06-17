package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/config"
)

// TestSetAuthCookiesProductionAttributes asserts that in production the cookies
// are Secure and scoped to the configured base domain (shared across subdomains).
func TestSetAuthCookiesProductionAttributes(t *testing.T) {
	cfg := &config.Config{Env: "production", BaseDomain: "vortex.v60ai.com", JWTAccessTTL: 15, JWTRefreshTTL: 24}
	rec := httptest.NewRecorder()
	setAuthCookies(rec, "atok", "rtok", cfg)
	for _, name := range []string{accessCookieName, refreshCookieName} {
		c := cookieByName(rec, name)
		if c == nil {
			t.Fatalf("missing cookie %q", name)
		}
		if !c.Secure {
			t.Fatalf("cookie %q should be Secure in production", name)
		}
		if c.Domain != "vortex.v60ai.com" {
			t.Fatalf("cookie %q Domain = %q, want base domain", name, c.Domain)
		}
		if !c.HttpOnly {
			t.Fatalf("cookie %q should be HttpOnly", name)
		}
	}
	// Access cookie MaxAge mirrors access TTL (minutes -> seconds).
	if got := cookieByName(rec, accessCookieName).MaxAge; got != 15*60 {
		t.Fatalf("access MaxAge = %d, want %d", got, 15*60)
	}
	if got := cookieByName(rec, refreshCookieName).MaxAge; got != 24*3600 {
		t.Fatalf("refresh MaxAge = %d, want %d", got, 24*3600)
	}
}

// cookieByName returns the named cookie set on the response, or nil.
func cookieByName(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// TestAuthCookiesSetOnSignupLoginRefresh asserts the access and refresh cookies
// are set (HttpOnly, correct names) on signup, login and refresh responses, and
// that tokens are still returned in the JSON body (for the CLI).
func TestAuthCookiesSetOnSignupLoginRefresh(t *testing.T) {
	s := newTestServer(t, "http://unused")

	assertCookies := func(t *testing.T, rec *httptest.ResponseRecorder) {
		t.Helper()
		for _, name := range []string{accessCookieName, refreshCookieName} {
			c := cookieByName(rec, name)
			if c == nil {
				t.Fatalf("missing cookie %q", name)
			}
			if !c.HttpOnly {
				t.Fatalf("cookie %q should be HttpOnly", name)
			}
			if c.SameSite != http.SameSiteLaxMode {
				t.Fatalf("cookie %q SameSite = %v, want Lax", name, c.SameSite)
			}
			if c.Path != "/" {
				t.Fatalf("cookie %q path = %q, want /", name, c.Path)
			}
			if c.Value == "" {
				t.Fatalf("cookie %q has empty value", name)
			}
			// Dev (non-production) config: cookies are host-only and not Secure.
			if c.Secure {
				t.Fatalf("cookie %q should not be Secure in dev", name)
			}
			if c.Domain != "" {
				t.Fatalf("cookie %q Domain = %q, want empty in dev", name, c.Domain)
			}
		}
	}

	// Signup sets cookies and returns body tokens.
	rec := doJSON(t, s, http.MethodPost, "/v1/auth/signup",
		`{"email":"cookie@example.com","name":"C","password":"supersecret"}`, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("signup = %d", rec.Code)
	}
	assertCookies(t, rec)
	var a authResponse
	_ = json.NewDecoder(rec.Body).Decode(&a)
	if a.AccessToken == "" || a.RefreshToken == "" {
		t.Fatal("expected body tokens for CLI compatibility")
	}

	// Login sets cookies.
	rec = doJSON(t, s, http.MethodPost, "/v1/auth/login",
		`{"email":"cookie@example.com","password":"supersecret"}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("login = %d", rec.Code)
	}
	assertCookies(t, rec)

	// Refresh (via body) sets new cookies.
	rec = doJSON(t, s, http.MethodPost, "/v1/auth/refresh",
		`{"refreshToken":"`+a.RefreshToken+`"}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh = %d, body=%s", rec.Code, rec.Body.String())
	}
	assertCookies(t, rec)
}

// TestMiddlewareAuthViaCookie asserts the auth middleware authenticates using the
// access cookie (browser flow), in addition to the Authorization header.
func TestMiddlewareAuthViaCookie(t *testing.T) {
	s := newTestServer(t, "http://unused")
	rec := doJSON(t, s, http.MethodPost, "/v1/auth/signup",
		`{"email":"viacookie@example.com","name":"V","password":"supersecret"}`, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("signup = %d", rec.Code)
	}
	access := cookieByName(rec, accessCookieName)
	if access == nil {
		t.Fatal("no access cookie")
	}

	// /me using ONLY the cookie (no Authorization header).
	req := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	req.AddCookie(access)
	mrec := httptest.NewRecorder()
	s.Router().ServeHTTP(mrec, req)
	if mrec.Code != http.StatusOK {
		t.Fatalf("me via cookie = %d, body=%s", mrec.Code, mrec.Body.String())
	}

	// Back-compat: /me via Authorization header still works.
	var a authResponse
	_ = json.NewDecoder(rec.Body).Decode(&a)
	if hrec := doJSON(t, s, http.MethodGet, "/v1/me", "", a.AccessToken); hrec.Code != http.StatusOK {
		t.Fatalf("me via header = %d", hrec.Code)
	}
}

// TestRefreshRotationHTTP asserts HTTP refresh rotates the refresh token: the old
// one is rejected (401) afterwards.
func TestRefreshRotationHTTP(t *testing.T) {
	s := newTestServer(t, "http://unused")
	rec := doJSON(t, s, http.MethodPost, "/v1/auth/signup",
		`{"email":"rothttp@example.com","name":"R","password":"supersecret"}`, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("signup = %d", rec.Code)
	}
	var a authResponse
	_ = json.NewDecoder(rec.Body).Decode(&a)

	// First refresh succeeds.
	rec = doJSON(t, s, http.MethodPost, "/v1/auth/refresh",
		`{"refreshToken":"`+a.RefreshToken+`"}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh = %d", rec.Code)
	}
	// Reusing the OLD refresh token is rejected (rotation + revocation).
	rec = doJSON(t, s, http.MethodPost, "/v1/auth/refresh",
		`{"refreshToken":"`+a.RefreshToken+`"}`, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("reused refresh = %d, want 401", rec.Code)
	}
}

// TestRefreshViaCookieHTTP asserts refresh reads the token from the cookie when no
// body is sent.
func TestRefreshViaCookieHTTP(t *testing.T) {
	s := newTestServer(t, "http://unused")
	rec := doJSON(t, s, http.MethodPost, "/v1/auth/signup",
		`{"email":"refcookie@example.com","name":"R","password":"supersecret"}`, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("signup = %d", rec.Code)
	}
	refresh := cookieByName(rec, refreshCookieName)
	if refresh == nil {
		t.Fatal("no refresh cookie")
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/auth/refresh", nil)
	req.AddCookie(refresh)
	rrec := httptest.NewRecorder()
	s.Router().ServeHTTP(rrec, req)
	if rrec.Code != http.StatusOK {
		t.Fatalf("refresh via cookie = %d, body=%s", rrec.Code, rrec.Body.String())
	}
	if cookieByName(rrec, refreshCookieName) == nil {
		t.Fatal("rotation should set a new refresh cookie")
	}
}

// TestLogoutHTTP asserts logout revokes the refresh token and clears both cookies.
func TestLogoutHTTP(t *testing.T) {
	s := newTestServer(t, "http://unused")
	rec := doJSON(t, s, http.MethodPost, "/v1/auth/signup",
		`{"email":"logouthttp@example.com","name":"L","password":"supersecret"}`, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("signup = %d", rec.Code)
	}
	access := cookieByName(rec, accessCookieName)
	refresh := cookieByName(rec, refreshCookieName)
	var a authResponse
	_ = json.NewDecoder(rec.Body).Decode(&a)

	// Logout (authenticated via cookie) clears both cookies.
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/logout", nil)
	req.AddCookie(access)
	req.AddCookie(refresh)
	lrec := httptest.NewRecorder()
	s.Router().ServeHTTP(lrec, req)
	if lrec.Code != http.StatusNoContent {
		t.Fatalf("logout = %d, body=%s", lrec.Code, lrec.Body.String())
	}
	for _, name := range []string{accessCookieName, refreshCookieName} {
		c := cookieByName(lrec, name)
		if c == nil || c.MaxAge >= 0 {
			t.Fatalf("logout should clear cookie %q (got %+v)", name, c)
		}
	}

	// The revoked refresh token can no longer be used.
	if r := doJSON(t, s, http.MethodPost, "/v1/auth/refresh",
		`{"refreshToken":"`+a.RefreshToken+`"}`, ""); r.Code != http.StatusUnauthorized {
		t.Fatalf("refresh after logout = %d, want 401", r.Code)
	}
}

// TestLogoutRequiresAuth asserts logout is an authenticated endpoint.
func TestLogoutRequiresAuth(t *testing.T) {
	s := newTestServer(t, "http://unused")
	if rec := doJSON(t, s, http.MethodPost, "/v1/auth/logout", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("logout without auth = %d, want 401", rec.Code)
	}
}
