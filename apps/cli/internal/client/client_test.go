package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// memTokens is an in-memory TokenStore for tests.
type memTokens struct {
	mu      sync.Mutex
	access  string
	refresh string
	saves   int
}

func (m *memTokens) Access() string  { m.mu.Lock(); defer m.mu.Unlock(); return m.access }
func (m *memTokens) Refresh() string { m.mu.Lock(); defer m.mu.Unlock(); return m.refresh }
func (m *memTokens) Save(a, r string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.access, m.refresh, m.saves = a, r, m.saves+1
	return nil
}

func TestLogin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/auth/login" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var body loginRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Email != "a@b.com" || body.Password != "secret" {
			t.Errorf("unexpected login body: %+v", body)
		}
		writeJSON(w, http.StatusOK, authResponse{
			User:         User{ID: "u1", Email: body.Email},
			AccessToken:  "acc",
			RefreshToken: "ref",
		})
	}))
	defer srv.Close()

	c := New(srv.URL, &memTokens{})
	res, err := c.Login(context.Background(), "a@b.com", "secret")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if res.AccessToken != "acc" || res.RefreshToken != "ref" || res.User.ID != "u1" {
		t.Fatalf("unexpected auth result: %+v", res)
	}
}

func TestListAppsAttachesBearer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer acc" {
			t.Errorf("missing/incorrect bearer: %q", got)
		}
		if r.URL.Path != "/v1/orgs/org1/apps" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		writeJSON(w, http.StatusOK, dataEnvelope[App]{Data: []App{
			{ID: "app1", Name: "web", Status: "running"},
		}})
	}))
	defer srv.Close()

	c := New(srv.URL, &memTokens{access: "acc"})
	apps, err := c.ListApps(context.Background(), "org1")
	if err != nil {
		t.Fatalf("ListApps: %v", err)
	}
	if len(apps) != 1 || apps[0].ID != "app1" {
		t.Fatalf("unexpected apps: %+v", apps)
	}
}

func TestRefreshOn401(t *testing.T) {
	var meCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/me":
			meCalls++
			// First call with stale token -> 401; after refresh -> 200.
			if r.Header.Get("Authorization") == "Bearer stale" {
				writeJSON(w, http.StatusUnauthorized, errBody("invalid or expired token"))
				return
			}
			if r.Header.Get("Authorization") != "Bearer fresh" {
				t.Errorf("expected refreshed token, got %q", r.Header.Get("Authorization"))
			}
			writeJSON(w, http.StatusOK, User{ID: "u1", Email: "a@b.com"})
		case "/v1/auth/refresh":
			var body refreshRequest
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.RefreshToken != "ref" {
				t.Errorf("unexpected refresh token: %q", body.RefreshToken)
			}
			writeJSON(w, http.StatusOK, authResponse{AccessToken: "fresh", RefreshToken: "ref2"})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	tokens := &memTokens{access: "stale", refresh: "ref"}
	c := New(srv.URL, tokens)
	u, err := c.Me(context.Background())
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	if u.ID != "u1" {
		t.Fatalf("unexpected user: %+v", u)
	}
	if meCalls != 2 {
		t.Fatalf("expected /v1/me called twice (pre + post refresh), got %d", meCalls)
	}
	if tokens.Access() != "fresh" || tokens.Refresh() != "ref2" {
		t.Fatalf("tokens not refreshed: %+v", tokens)
	}
	if tokens.saves != 1 {
		t.Fatalf("expected exactly one token save, got %d", tokens.saves)
	}
}

func TestRefreshGivesUpWithoutRefreshToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusUnauthorized, errBody("missing bearer token"))
	}))
	defer srv.Close()

	c := New(srv.URL, &memTokens{access: "stale"}) // no refresh token
	_, err := c.Me(context.Background())
	if !IsUnauthorized(err) {
		t.Fatalf("expected unauthorized error, got %v", err)
	}
}

func TestAPIErrorMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusConflict, errBody("email already registered"))
	}))
	defer srv.Close()

	c := New(srv.URL, nil)
	_, err := c.Signup(context.Background(), "a@b.com", "A", "pw")
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "email already registered" {
		t.Fatalf("unexpected error message: %q", err.Error())
	}
}

func TestServiceCatalogNoAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("catalog should not send auth header, got %q", r.Header.Get("Authorization"))
		}
		writeJSON(w, http.StatusOK, dataEnvelope[ServiceTemplate]{Data: []ServiceTemplate{
			{Key: "wordpress", Name: "WordPress", Kind: "service"},
		}})
	}))
	defer srv.Close()

	c := New(srv.URL, &memTokens{access: "acc"})
	items, err := c.ServiceCatalog(context.Background())
	if err != nil {
		t.Fatalf("ServiceCatalog: %v", err)
	}
	if len(items) != 1 || items[0].Key != "wordpress" {
		t.Fatalf("unexpected catalog: %+v", items)
	}
}

func TestSetEnvAndUnset(t *testing.T) {
	var deleted bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/v1/orgs/o/apps/a/env":
			var body setEnvRequest
			_ = json.NewDecoder(r.Body).Decode(&body)
			writeJSON(w, http.StatusOK, EnvVar{Key: body.Key, Value: body.Value})
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/orgs/o/apps/a/env/FOO":
			deleted = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, &memTokens{access: "acc"})
	ev, err := c.SetEnv(context.Background(), "o", "a", "FOO", "bar")
	if err != nil {
		t.Fatalf("SetEnv: %v", err)
	}
	if ev.Key != "FOO" || ev.Value != "bar" {
		t.Fatalf("unexpected env var: %+v", ev)
	}
	if err := c.UnsetEnv(context.Background(), "o", "a", "FOO"); err != nil {
		t.Fatalf("UnsetEnv: %v", err)
	}
	if !deleted {
		t.Fatal("expected delete to be invoked")
	}
}

// errBody builds the API's error JSON shape.
func errBody(msg string) map[string]string { return map[string]string{"error": msg} }

// writeJSON mirrors the server's JSON writer for tests.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
