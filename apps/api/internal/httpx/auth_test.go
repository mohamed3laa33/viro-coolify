package httpx

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
