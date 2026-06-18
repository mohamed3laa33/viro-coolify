package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/config"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/kube"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/store"
)

// newTestServer builds a Server backed by the in-memory store and FakeBackend.
// The legacy upstream-URL parameter is retained for call-site compatibility but
// is now unused (the Coolify backend was removed).
func newTestServer(t *testing.T, _ string) *Server {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	// Control-plane HTTP tests inject the in-memory FakeBackend (a real test
	// double for kube.Backend) so resource creation is deterministic and never
	// touches a real cluster.
	return NewServer(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)),
		store.NewMemoryStore(), WithBackend(kube.NewFakeBackend()),
		// Hermetic custom-domain verification: an empty-TXT resolver never matches,
		// so tests never touch real DNS (and a verify resolves to "failed").
		WithDomainResolver(emptyResolver{}))
}

// emptyResolver is a platform.Resolver that returns no TXT records, so custom-
// domain verification deterministically fails without any real DNS lookup.
type emptyResolver struct{}

func (emptyResolver) LookupTXT(_ context.Context, _ string) ([]string, error) { return nil, nil }

// signup registers a user and returns their access token (for authenticated requests).
func signup(t *testing.T, s *Server, email string) string {
	t.Helper()
	rec := doJSON(t, s, http.MethodPost, "/v1/auth/signup",
		`{"email":"`+email+`","name":"T","password":"supersecret"}`, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("signup helper: %d %s", rec.Code, rec.Body.String())
	}
	var a authResponse
	if err := json.NewDecoder(rec.Body).Decode(&a); err != nil {
		t.Fatalf("signup helper decode: %v", err)
	}
	return a.AccessToken
}

func TestHealthz(t *testing.T) {
	s := newTestServer(t, "http://unused")
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("status field = %q", body["status"])
	}
}

func TestReadyzMemoryStoreOK(t *testing.T) {
	// The in-memory store is not a Pinger, so readiness is always ok.
	s := newTestServer(t, "http://unused")
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
	}
	if rid := rec.Header().Get("X-Request-Id"); rid == "" {
		t.Fatalf("expected X-Request-Id response header")
	}
}

// failingPingStore embeds the memory store but reports an unhealthy dependency.
type failingPingStore struct {
	store.Store
}

func (failingPingStore) Ping(context.Context) error {
	return errors.New("db down")
}

func TestReadyzPingFailureReturns503(t *testing.T) {
	s := newTestServer(t, "http://unused")
	s.store = failingPingStore{Store: store.NewMemoryStore()}
	// Rebuild routes so the readiness handler closes over the swapped store.
	s.router = s.routes()

	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestVersionEndpoint(t *testing.T) {
	s := newTestServer(t, "http://unused")
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/version", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["service"] != "vortex-api" {
		t.Fatalf("service = %v", body["service"])
	}
}

// firstOrgID returns the id of the user's first (personal) organization.
func firstOrgID(t *testing.T, s *Server, token string) string {
	t.Helper()
	rec := doJSON(t, s, http.MethodGet, "/v1/orgs/", "", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("list orgs: %d", rec.Code)
	}
	var resp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode orgs: %v", err)
	}
	if len(resp.Data) == 0 {
		t.Fatal("expected at least one org")
	}
	return resp.Data[0].ID
}

func TestAppLifecycleOrgScoped(t *testing.T) {
	s := newTestServer(t, "http://unused")
	token := signup(t, s, "owner-apps@example.com")
	org := firstOrgID(t, s, token)

	// Image-based app so a redeploy has an image to apply (a git app with no
	// image yet would correctly get a 409 from /deploy).
	rec := doJSON(t, s, http.MethodPost, "/v1/orgs/"+org+"/apps", `{"name":"web","image":"nginx:1.27"}`, token)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create app: %d %s", rec.Code, rec.Body.String())
	}
	var app struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&app)
	if app.ID == "" || app.Status != "deploying" {
		t.Fatalf("unexpected app: %+v", app)
	}

	rec = doJSON(t, s, http.MethodGet, "/v1/orgs/"+org+"/apps", "", token)
	var list struct {
		Data []json.RawMessage `json:"data"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&list)
	if len(list.Data) != 1 {
		t.Fatalf("expected 1 app, got %d", len(list.Data))
	}

	rec = doJSON(t, s, http.MethodPost, "/v1/orgs/"+org+"/apps/"+app.ID+"/deploy", "", token)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("deploy: %d %s", rec.Code, rec.Body.String())
	}
	var deployed struct {
		Status string `json:"status"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&deployed)
	if deployed.Status != "deploying" {
		t.Fatalf("status = %q", deployed.Status)
	}
}

func TestAppsAreTenantScoped(t *testing.T) {
	s := newTestServer(t, "http://unused")
	a := signup(t, s, "tenant-a@example.com")
	orgA := firstOrgID(t, s, a)
	b := signup(t, s, "tenant-b@example.com")
	orgB := firstOrgID(t, s, b)

	if rec := doJSON(t, s, http.MethodPost, "/v1/orgs/"+orgA+"/apps", `{"name":"secret"}`, a); rec.Code != http.StatusCreated {
		t.Fatalf("create app in A: %d", rec.Code)
	}

	// B's own org shows none of A's apps.
	rec := doJSON(t, s, http.MethodGet, "/v1/orgs/"+orgB+"/apps", "", b)
	var list struct {
		Data []json.RawMessage `json:"data"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&list)
	if len(list.Data) != 0 {
		t.Fatalf("expected 0 apps in B's org, got %d", len(list.Data))
	}

	// B cannot read A's org at all (not a member).
	if rec := doJSON(t, s, http.MethodGet, "/v1/orgs/"+orgA+"/apps", "", b); rec.Code != http.StatusForbidden {
		t.Fatalf("cross-tenant read = %d, want 403", rec.Code)
	}
}

func TestMemberCanReadButCannotMutate(t *testing.T) {
	s := newTestServer(t, "http://unused")
	owner := signup(t, s, "owner2@example.com")
	org := firstOrgID(t, s, owner)
	member := signup(t, s, "member2@example.com")

	meRec := doJSON(t, s, http.MethodGet, "/v1/me", "", member)
	var me struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(meRec.Body).Decode(&me)
	if err := s.store.AddMembership(context.Background(), domain.Membership{OrgID: org, UserID: me.ID, Role: domain.RoleMember}); err != nil {
		t.Fatalf("add membership: %v", err)
	}

	// Member (read) can list.
	if rec := doJSON(t, s, http.MethodGet, "/v1/orgs/"+org+"/apps", "", member); rec.Code != http.StatusOK {
		t.Fatalf("member read = %d, want 200", rec.Code)
	}
	// Member cannot create (needs admin).
	if rec := doJSON(t, s, http.MethodPost, "/v1/orgs/"+org+"/apps", `{"name":"x"}`, member); rec.Code != http.StatusForbidden {
		t.Fatalf("member create = %d, want 403", rec.Code)
	}
}
