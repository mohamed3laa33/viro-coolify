package client

import (
	"context"
	"encoding/json"
	"fmt"
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

// patTokens is a TokenStore that also exposes a personal access token.
type patTokens struct {
	memTokens
	pat string
}

func (p *patTokens) PAT() string { return p.pat }

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

func TestPATAuthHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer vrt_secret" {
			t.Errorf("expected PAT bearer, got %q", got)
		}
		writeJSON(w, http.StatusOK, User{ID: "u1", Email: "a@b.com"})
	}))
	defer srv.Close()

	// The PAT must take precedence over any stale JWT access token.
	c := New(srv.URL, &patTokens{memTokens: memTokens{access: "jwt"}, pat: "vrt_secret"})
	if _, err := c.Me(context.Background()); err != nil {
		t.Fatalf("Me: %v", err)
	}
}

func TestPATNeverRefreshes(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/auth/refresh" {
			t.Fatal("PAT auth must never call refresh")
		}
		calls++
		writeJSON(w, http.StatusUnauthorized, errBody("invalid or expired token"))
	}))
	defer srv.Close()

	// A refresh token is present, but the PAT path must not use it on a 401.
	c := New(srv.URL, &patTokens{memTokens: memTokens{refresh: "ref"}, pat: "vrt_dead"})
	_, err := c.Me(context.Background())
	if !IsUnauthorized(err) {
		t.Fatalf("expected unauthorized, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected exactly one /me call (no refresh retry), got %d", calls)
	}
}

func TestCreateAndListTokens(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/tokens":
			var body createTokenRequest
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Name != "ci" || body.ExpiresInDays != 30 {
				t.Errorf("unexpected create body: %+v", body)
			}
			writeJSON(w, http.StatusCreated, ApiToken{
				ID: "t1", Name: body.Name, Token: "vrt_plaintext", Prefix: "vrt_plai", Scopes: body.Scopes,
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tokens":
			writeJSON(w, http.StatusOK, dataEnvelope[ApiToken]{Data: []ApiToken{
				{ID: "t1", Name: "ci", Prefix: "vrt_plai"},
			}})
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/tokens/t1":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, &memTokens{access: "acc"})
	tok, err := c.CreateToken(context.Background(), CreateTokenInput{Name: "ci", ExpiresInDays: 30, Scopes: []string{"deploy"}})
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	if tok.Token != "vrt_plaintext" {
		t.Fatalf("expected plaintext token once, got %q", tok.Token)
	}
	list, err := c.ListTokens(context.Background())
	if err != nil {
		t.Fatalf("ListTokens: %v", err)
	}
	if len(list) != 1 || list[0].Token != "" {
		t.Fatalf("listing must not carry the secret: %+v", list)
	}
	if err := c.RevokeToken(context.Background(), "t1"); err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}
}

func TestListReleasesAndRollback(t *testing.T) {
	var rolledTo int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/orgs/o/apps/a/releases":
			writeJSON(w, http.StatusOK, dataEnvelope[Release]{Data: []Release{
				{Revision: 2, Status: "active", Image: "img:2"},
				{Revision: 1, Status: "superseded", Image: "img:1"},
			}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/orgs/o/apps/a/rollback":
			var body rollbackRequest
			_ = json.NewDecoder(r.Body).Decode(&body)
			rolledTo = body.Revision
			writeJSON(w, http.StatusAccepted, App{ID: "a", Name: "web", Status: "deploying"})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, &memTokens{access: "acc"})
	rels, err := c.ListReleases(context.Background(), "o", "a")
	if err != nil {
		t.Fatalf("ListReleases: %v", err)
	}
	if len(rels) != 2 || rels[0].Revision != 2 {
		t.Fatalf("unexpected releases: %+v", rels)
	}
	if _, err := c.Rollback(context.Background(), "o", "a", 1); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if rolledTo != 1 {
		t.Fatalf("expected rollback to revision 1, got %d", rolledTo)
	}
}

func TestScaleApp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/orgs/o/apps/a/scale" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		var body scaleAppRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.MinReplicas == nil || *body.MinReplicas != 1 {
			t.Errorf("expected min=1, got %+v", body.MinReplicas)
		}
		if body.MaxReplicas == nil || *body.MaxReplicas != 5 {
			t.Errorf("expected max=5, got %+v", body.MaxReplicas)
		}
		writeJSON(w, http.StatusAccepted, App{ID: "a", Name: "web", MinReplicas: 1, MaxReplicas: 5})
	}))
	defer srv.Close()

	c := New(srv.URL, &memTokens{access: "acc"})
	mn, mx := 1, 5
	app, err := c.ScaleApp(context.Background(), "o", "a", ScaleAppInput{MinReplicas: &mn, MaxReplicas: &mx})
	if err != nil {
		t.Fatalf("ScaleApp: %v", err)
	}
	if app.MinReplicas != 1 || app.MaxReplicas != 5 {
		t.Fatalf("unexpected scale result: %+v", app)
	}
}

func TestUpdateAppPatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/v1/orgs/o/apps/a" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		var body updateAppRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Image == nil || *body.Image != "img:3" {
			t.Errorf("expected image patch, got %+v", body.Image)
		}
		if body.CPU != nil {
			t.Errorf("CPU should be omitted, got %+v", body.CPU)
		}
		writeJSON(w, http.StatusOK, App{ID: "a", Name: "web", Image: "img:3"})
	}))
	defer srv.Close()

	c := New(srv.URL, &memTokens{access: "acc"})
	img := "img:3"
	app, err := c.UpdateApp(context.Background(), "o", "a", UpdateAppInput{Image: &img})
	if err != nil {
		t.Fatalf("UpdateApp: %v", err)
	}
	if app.Image != "img:3" {
		t.Fatalf("unexpected update result: %+v", app)
	}
}

func TestGetDatabaseConnInfo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/orgs/o/databases/d1" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		writeJSON(w, http.StatusOK, DatabaseDetail{
			Database: Database{ID: "d1", Name: "pg", Engine: "postgresql", Status: "running"},
			Connection: DatabaseConnInfo{
				Host: "pg.ns.svc.cluster.local", Port: 5432, Database: "app",
				Username: "u", Password: "p", ConnectionString: "postgres://u:p@pg.ns.svc.cluster.local:5432/app",
			},
		})
	}))
	defer srv.Close()

	c := New(srv.URL, &memTokens{access: "acc"})
	d, err := c.GetDatabase(context.Background(), "o", "d1")
	if err != nil {
		t.Fatalf("GetDatabase: %v", err)
	}
	if d.Connection.Port != 5432 || d.Connection.Password != "p" || d.Connection.ConnectionString == "" {
		t.Fatalf("unexpected conn info: %+v", d.Connection)
	}
}

func TestAddAndVerifyDomain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/orgs/o/apps/a/domains":
			writeJSON(w, http.StatusCreated, DomainResult{
				Domain:       Domain{ID: "dom1", Domain: "x.example.com", Status: "pending"},
				Instructions: DomainInstructions{TXTName: "_vortex-challenge.x.example.com", TXTValue: "tok", TargetType: "CNAME", TargetValue: "app.host"},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/orgs/o/apps/a/domains/dom1/verify":
			writeJSON(w, http.StatusOK, DomainResult{
				Domain: Domain{ID: "dom1", Domain: "x.example.com", Status: "verified", Verified: true},
			})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, &memTokens{access: "acc"})
	add, err := c.AddDomain(context.Background(), "o", "a", "x.example.com")
	if err != nil {
		t.Fatalf("AddDomain: %v", err)
	}
	if add.Instructions.TXTValue != "tok" || add.Instructions.TargetType != "CNAME" {
		t.Fatalf("missing DNS instructions: %+v", add.Instructions)
	}
	ver, err := c.VerifyDomain(context.Background(), "o", "a", "dom1")
	if err != nil {
		t.Fatalf("VerifyDomain: %v", err)
	}
	if ver.Domain.Status != "verified" {
		t.Fatalf("expected verified, got %q", ver.Domain.Status)
	}
}

func TestFollowAppLogsConsumesStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/orgs/o/apps/a/logs" || r.URL.Query().Get("follow") != "true" {
			t.Errorf("unexpected stream request %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		if r.Header.Get("Authorization") != "Bearer acc" {
			t.Errorf("stream missing bearer: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		f, _ := w.(http.Flusher)
		for i := 1; i <= 3; i++ {
			fmt.Fprintf(w, "data: line %d\n\n", i)
		}
		if f != nil {
			f.Flush()
		}
	}))
	defer srv.Close()

	c := New(srv.URL, &memTokens{access: "acc"})
	var got []string
	err := c.FollowAppLogs(context.Background(), "o", "a", false, func(line string) {
		got = append(got, line)
	})
	if err != nil {
		t.Fatalf("FollowAppLogs: %v", err)
	}
	if len(got) != 3 || got[0] != "line 1" || got[2] != "line 3" {
		t.Fatalf("unexpected streamed lines: %+v", got)
	}
}

func TestListBuildsAndMetrics(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/orgs/o/apps/a/builds":
			writeJSON(w, http.StatusOK, dataEnvelope[Build]{Data: []Build{
				{ID: "b1", Status: "succeeded", Image: "img:1"},
			}})
		case "/v1/orgs/o/apps/a/metrics":
			writeJSON(w, http.StatusOK, Metrics{
				Available: true, CPUMillicores: 120, MemoryBytes: 1048576,
				Pods: []PodMetric{{Name: "p1", CPUMillicores: 120, MemoryBytes: 1048576}},
			})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, &memTokens{access: "acc"})
	builds, err := c.ListBuilds(context.Background(), "o", "a")
	if err != nil {
		t.Fatalf("ListBuilds: %v", err)
	}
	if len(builds) != 1 || builds[0].Status != "succeeded" {
		t.Fatalf("unexpected builds: %+v", builds)
	}
	m, err := c.AppMetrics(context.Background(), "o", "a")
	if err != nil {
		t.Fatalf("AppMetrics: %v", err)
	}
	if !m.Available || m.CPUMillicores != 120 || len(m.Pods) != 1 {
		t.Fatalf("unexpected metrics: %+v", m)
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
