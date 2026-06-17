package httpx

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func doJSON(t *testing.T, s *Server, method, path, body, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	return rec
}

func TestSignupThenMe(t *testing.T) {
	s := newTestServer(t, "http://unused")

	rec := doJSON(t, s, http.MethodPost, "/v1/auth/signup",
		`{"email":"alice@example.com","name":"Alice","password":"supersecret"}`, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("signup status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var auth authResponse
	if err := json.NewDecoder(rec.Body).Decode(&auth); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if auth.AccessToken == "" || auth.User.Email != "alice@example.com" {
		t.Fatalf("unexpected auth response: %+v", auth)
	}

	// /me without a token is unauthorized.
	if rec := doJSON(t, s, http.MethodGet, "/v1/me", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("me without token = %d, want 401", rec.Code)
	}

	// /me with the access token works.
	rec = doJSON(t, s, http.MethodGet, "/v1/me", "", auth.AccessToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("me status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var me userView
	if err := json.NewDecoder(rec.Body).Decode(&me); err != nil {
		t.Fatalf("decode me: %v", err)
	}
	if me.Email != "alice@example.com" {
		t.Fatalf("me email = %q", me.Email)
	}
}

func TestSignupDuplicateConflicts(t *testing.T) {
	s := newTestServer(t, "http://unused")
	body := `{"email":"bob@example.com","name":"Bob","password":"supersecret"}`
	if rec := doJSON(t, s, http.MethodPost, "/v1/auth/signup", body, ""); rec.Code != http.StatusCreated {
		t.Fatalf("first signup = %d", rec.Code)
	}
	if rec := doJSON(t, s, http.MethodPost, "/v1/auth/signup", body, ""); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate signup = %d, want 409", rec.Code)
	}
}

func TestLoginAndCreateOrg(t *testing.T) {
	s := newTestServer(t, "http://unused")
	doJSON(t, s, http.MethodPost, "/v1/auth/signup",
		`{"email":"carol@example.com","name":"Carol","password":"supersecret"}`, "")

	rec := doJSON(t, s, http.MethodPost, "/v1/auth/login",
		`{"email":"carol@example.com","password":"supersecret"}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("login = %d", rec.Code)
	}
	var auth authResponse
	_ = json.NewDecoder(rec.Body).Decode(&auth)

	// Create a new organization with the access token.
	rec = doJSON(t, s, http.MethodPost, "/v1/orgs/", `{"name":"Acme Inc"}`, auth.AccessToken)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create org = %d, body = %s", rec.Code, rec.Body.String())
	}

	// List orgs: should now include the personal org + Acme = 2.
	rec = doJSON(t, s, http.MethodGet, "/v1/orgs/", "", auth.AccessToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("list orgs = %d", rec.Code)
	}
	var listed struct {
		Data []json.RawMessage `json:"data"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&listed)
	if len(listed.Data) != 2 {
		t.Fatalf("expected 2 orgs, got %d", len(listed.Data))
	}
}

func TestLoginWrongPassword(t *testing.T) {
	s := newTestServer(t, "http://unused")
	doJSON(t, s, http.MethodPost, "/v1/auth/signup",
		`{"email":"dan@example.com","name":"Dan","password":"supersecret"}`, "")
	rec := doJSON(t, s, http.MethodPost, "/v1/auth/login",
		`{"email":"dan@example.com","password":"nope"}`, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("login wrong password = %d, want 401", rec.Code)
	}
}

// TestSignupValidationHTTP asserts the HTTP status contract for signup input.
func TestSignupValidationHTTP(t *testing.T) {
	cases := []struct {
		name string
		body string
		want int
	}{
		{"valid", `{"email":"ok@example.com","name":"Ok","password":"supersecret"}`, http.StatusCreated},
		{"weak password", `{"email":"weak@example.com","name":"W","password":"short"}`, http.StatusBadRequest},
		{"empty password", `{"email":"empty@example.com","name":"E","password":""}`, http.StatusBadRequest},
		{"invalid email", `{"email":"not-an-email","name":"N","password":"supersecret"}`, http.StatusBadRequest},
		{"password over 72 bytes", `{"email":"long@example.com","name":"L","password":"` + strings.Repeat("a", 73) + `"}`, http.StatusBadRequest},
		{"malformed json", `{"email":`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestServer(t, "http://unused")
			rec := doJSON(t, s, http.MethodPost, "/v1/auth/signup", tc.body, "")
			if rec.Code != tc.want {
				t.Fatalf("signup %s = %d, want %d (body: %s)", tc.name, rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

// TestLoginUnknownUserHTTP asserts login for a non-existent user returns 401
// (not 404), so user existence is not disclosed.
func TestLoginUnknownUserHTTP(t *testing.T) {
	s := newTestServer(t, "http://unused")
	rec := doJSON(t, s, http.MethodPost, "/v1/auth/login",
		`{"email":"ghost@example.com","password":"supersecret"}`, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("login unknown user = %d, want 401", rec.Code)
	}
}

// TestRefreshFlowHTTP covers refresh success and rejection of invalid/non-refresh tokens.
func TestRefreshFlowHTTP(t *testing.T) {
	s := newTestServer(t, "http://unused")
	rec := doJSON(t, s, http.MethodPost, "/v1/auth/signup",
		`{"email":"refresh@example.com","name":"R","password":"supersecret"}`, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("signup = %d", rec.Code)
	}
	var a authResponse
	if err := json.NewDecoder(rec.Body).Decode(&a); err != nil {
		t.Fatalf("decode signup: %v", err)
	}

	// Valid refresh token yields a new pair.
	rec = doJSON(t, s, http.MethodPost, "/v1/auth/refresh",
		`{"refreshToken":"`+a.RefreshToken+`"}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh = %d, body = %s", rec.Code, rec.Body.String())
	}
	var refreshed authResponse
	if err := json.NewDecoder(rec.Body).Decode(&refreshed); err != nil {
		t.Fatalf("decode refresh: %v", err)
	}
	if refreshed.AccessToken == "" || refreshed.RefreshToken == "" {
		t.Fatalf("expected a new token pair, got %+v", refreshed)
	}

	// An access token must not be accepted as a refresh token -> 401.
	if rec := doJSON(t, s, http.MethodPost, "/v1/auth/refresh",
		`{"refreshToken":"`+a.AccessToken+`"}`, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("access-as-refresh = %d, want 401", rec.Code)
	}

	// Garbage refresh token -> 401.
	if rec := doJSON(t, s, http.MethodPost, "/v1/auth/refresh",
		`{"refreshToken":"not.a.jwt"}`, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("garbage refresh = %d, want 401", rec.Code)
	}
}

// TestMeInvalidTokenHTTP asserts /me rejects malformed and missing bearer tokens.
func TestMeInvalidTokenHTTP(t *testing.T) {
	s := newTestServer(t, "http://unused")
	if rec := doJSON(t, s, http.MethodGet, "/v1/me", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("me missing token = %d, want 401", rec.Code)
	}
	if rec := doJSON(t, s, http.MethodGet, "/v1/me", "", "garbage.token.value"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("me garbage token = %d, want 401", rec.Code)
	}
}
