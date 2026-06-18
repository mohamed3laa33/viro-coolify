package httpx

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/config"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/kube"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/store"
)

// TestMetricsEndpointTextFormat asserts GET /metrics returns the Prometheus 0.0.4
// text exposition format with the expected HELP/TYPE lines and content type.
func TestMetricsEndpointTextFormat(t *testing.T) {
	s := newTestServer(t, "http://unused")

	rec := httptest.NewRecorder()
	s.MetricsHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics = %d %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain; version=0.0.4") {
		t.Fatalf("content-type = %q, want prometheus text exposition", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"# TYPE http_requests_total counter",
		"# TYPE http_request_duration_seconds histogram",
		"# TYPE http_requests_in_flight gauge",
		"# TYPE vortex_build_info gauge",
		"vortex_db_up ",
		"# TYPE vortex_helm_exec_total counter",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics body missing %q\n---\n%s", want, body)
		}
	}
}

// TestMetricsRecordsRequestWithRoutePattern asserts a recorded request increments
// http_requests_total and that the route LABEL is the chi route PATTERN (not the
// concrete path with the tenant id), so cardinality stays bounded.
func TestMetricsRecordsRequestWithRoutePattern(t *testing.T) {
	s := newTestServer(t, "http://unused")
	token := signup(t, s, "metrics-red@example.com")
	org := firstOrgID(t, s, token)

	// Drive a request through a parameterized route.
	rec := doJSON(t, s, http.MethodGet, "/v1/orgs/"+org+"/apps", "", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("list apps = %d %s", rec.Code, rec.Body.String())
	}

	mrec := httptest.NewRecorder()
	s.MetricsHandler().ServeHTTP(mrec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := mrec.Body.String()

	// The route label must be the PATTERN, and the concrete org id must NOT appear.
	wantSeries := `http_requests_total{method="GET",route="/v1/orgs/{orgID}/apps",status="200"}`
	if !strings.Contains(body, wantSeries) {
		t.Fatalf("missing RED series %q\n---\n%s", wantSeries, body)
	}
	if strings.Contains(body, "route=\"/v1/orgs/"+org) {
		t.Fatalf("route label leaked the concrete path/tenant id:\n%s", body)
	}
	// The per-route histogram must be present for that pattern too.
	if !strings.Contains(body, `http_request_duration_seconds_count{route="/v1/orgs/{orgID}/apps"}`) {
		t.Fatalf("missing duration histogram for the route pattern\n---\n%s", body)
	}
}

// TestMetricsTokenGate asserts that when VORTEX_METRICS_TOKEN is set the endpoint
// requires a matching Bearer token.
func TestMetricsTokenGate(t *testing.T) {
	s := newTestServer(t, "http://unused")
	s.cfg.MetricsToken = "s3cret-scrape"
	s.router = s.routes()

	// No token -> 401.
	rec := httptest.NewRecorder()
	s.MetricsHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated metrics = %d, want 401", rec.Code)
	}

	// Correct token -> 200.
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer s3cret-scrape")
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("authenticated metrics = %d, want 200", rec.Code)
	}
}

// TestMetricsMethodLabelBounded asserts the `method` label is collapsed to an
// allowlist ("other" for unknown verbs), so an attacker sending arbitrary HTTP
// methods cannot mint unbounded series (a cardinality / scrape-DoS bomb).
func TestMetricsMethodLabelBounded(t *testing.T) {
	s := newTestServer(t, "http://unused")

	// Drive a request with an arbitrary, attacker-controlled method.
	s.Router().ServeHTTP(httptest.NewRecorder(),
		httptest.NewRequest("QUUXZZZZ", "/healthz", nil))

	rec := httptest.NewRecorder()
	s.MetricsHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	if strings.Contains(body, "QUUXZZZZ") {
		t.Fatalf("raw attacker method leaked into a metrics label:\n%s", body)
	}
	if !strings.Contains(body, `method="other"`) {
		t.Fatalf("unknown method not collapsed to \"other\":\n%s", body)
	}
}

// flushRecorder is an httptest.ResponseRecorder that also implements http.Flusher
// and notifies on each flush, so the SSE streaming test can observe progress.
type flushRecorder struct {
	*httptest.ResponseRecorder
	flushed chan struct{}
}

func newFlushRecorder() *flushRecorder {
	return &flushRecorder{ResponseRecorder: httptest.NewRecorder(), flushed: make(chan struct{}, 64)}
}

func (f *flushRecorder) Flush() {
	f.ResponseRecorder.Flush()
	select {
	case f.flushed <- struct{}{}:
	default:
	}
}

// TestAppLogsFollowSSE asserts the follow endpoint streams the seeded log lines
// as Server-Sent Events (text/event-stream, one `data:` event per line). The
// FakeBackend writes its canned snapshot and returns, so the handler completes on
// its own — exercising the SSE framing end to end.
func TestAppLogsFollowSSE(t *testing.T) {
	s := newTestServer(t, "http://unused")
	token := signup(t, s, "logs-sse@example.com")
	org := firstOrgID(t, s, token)

	// Create an image-based app so it deploys directly and has a Release (the fake
	// backend returns canned log lines for a deployed release).
	rec := doJSON(t, s, http.MethodPost, "/v1/orgs/"+org+"/apps",
		`{"name":"web","image":"nginx:1.27"}`, token)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create app = %d %s", rec.Code, rec.Body.String())
	}
	var app struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&app)

	fr := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/v1/orgs/"+org+"/apps/"+app.ID+"/logs?follow=true", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	s.Router().ServeHTTP(fr, req)

	body := fr.Body.String()
	if !strings.Contains(body, "data: fake log line") {
		t.Fatalf("expected SSE data event with the seeded log line, got:\n%s", body)
	}
	if ct := fr.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q, want text/event-stream", ct)
	}
}

// blockingLogBackend is a kube.Backend whose LogStream BLOCKS until ctx is
// cancelled (after emitting one line), so the SSE handler's disconnect handling
// (cancel -> stop, no goroutine leak) can be asserted deterministically.
type blockingLogBackend struct {
	*kube.FakeBackend
	started chan struct{}
}

func (b *blockingLogBackend) LogStream(ctx context.Context, ns, release string, opts kube.LogStreamOptions, w io.Writer) error {
	_, _ = io.WriteString(w, "streaming line 1\n")
	close(b.started)
	<-ctx.Done() // follow until the client disconnects
	return ctx.Err()
}

// TestAppLogsFollowStopsOnDisconnect asserts the follow stream stops (handler
// returns, no goroutine leak) when the client context is cancelled mid-follow.
func TestAppLogsFollowStopsOnDisconnect(t *testing.T) {
	fb := kube.NewFakeBackend()
	be := &blockingLogBackend{FakeBackend: fb, started: make(chan struct{})}
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	s := NewServer(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)),
		store.NewMemoryStore(), WithBackend(be))

	token := signup(t, s, "logs-disc@example.com")
	org := firstOrgID(t, s, token)
	rec := doJSON(t, s, http.MethodPost, "/v1/orgs/"+org+"/apps",
		`{"name":"web","image":"nginx:1.27"}`, token)
	var app struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&app)

	ctx, cancel := context.WithCancel(context.Background())
	fr := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/v1/orgs/"+org+"/apps/"+app.ID+"/logs?follow=true", nil).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer "+token)

	done := make(chan struct{})
	go func() {
		s.Router().ServeHTTP(fr, req)
		close(done)
	}()

	// Wait until the backend stream has actually started (one line emitted), then
	// disconnect the client.
	select {
	case <-be.started:
	case <-time.After(2 * time.Second):
		t.Fatal("backend log stream never started")
	}
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not stop after client disconnect (goroutine leak)")
	}
	if !strings.Contains(fr.Body.String(), "data: streaming line 1") {
		t.Fatalf("expected the first streamed line as an SSE event, got:\n%s", fr.Body.String())
	}
}

// TestAppLogsSnapshotStillWorks asserts the non-follow endpoint still returns a
// JSON snapshot (the existing behavior is preserved).
func TestAppLogsSnapshotStillWorks(t *testing.T) {
	s := newTestServer(t, "http://unused")
	token := signup(t, s, "logs-snap@example.com")
	org := firstOrgID(t, s, token)

	rec := doJSON(t, s, http.MethodPost, "/v1/orgs/"+org+"/apps",
		`{"name":"web","image":"nginx:1.27"}`, token)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create app = %d %s", rec.Code, rec.Body.String())
	}
	var app struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&app)

	rec = doJSON(t, s, http.MethodGet, "/v1/orgs/"+org+"/apps/"+app.ID+"/logs", "", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("snapshot logs = %d %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("snapshot content-type = %q, want json", ct)
	}
	if !strings.Contains(rec.Body.String(), "fake log line") {
		t.Fatalf("snapshot body missing fake log line: %s", rec.Body.String())
	}
}
