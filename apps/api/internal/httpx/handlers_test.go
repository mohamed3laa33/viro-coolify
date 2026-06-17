package httpx

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/config"
)

// newTestServer builds a Server whose Coolify client points at the given upstream URL.
func newTestServer(t *testing.T, coolifyURL string) *Server {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	cfg.CoolifyBaseURL = coolifyURL
	cfg.CoolifyToken = "test-token"
	return NewServer(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

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
	if body["service"] != "viro-api" {
		t.Fatalf("service = %v", body["service"])
	}
}

func TestListAppsProxiesCoolify(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/applications" {
			t.Errorf("unexpected upstream path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"uuid":"abc","name":"web"}]`))
	}))
	defer upstream.Close()

	s := newTestServer(t, upstream.URL)
	token := signup(t, s, "apps-list@example.com")
	rec := doJSON(t, s, http.MethodGet, "/v1/apps/", "", token)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data []struct {
			UUID string `json:"uuid"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Data) != 1 || body.Data[0].UUID != "abc" {
		t.Fatalf("unexpected body: %+v", body)
	}
}

func TestListAppsUpstreamErrorReturns502(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	s := newTestServer(t, upstream.URL)
	token := signup(t, s, "apps-err@example.com")
	rec := doJSON(t, s, http.MethodGet, "/v1/apps/", "", token)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}
