package httpx

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/build"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/config"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/kube"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/store"
)

// newBuildServer builds a test Server with an injected FakeBuilder so the
// git→build→deploy flow is deterministic and WaitBuilds() can drain it.
func newBuildServer(t *testing.T, fb *build.FakeBuilder) *Server {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return NewServer(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)),
		store.NewMemoryStore(),
		WithBackend(kube.NewFakeBackend()),
		WithBuilder(fb),
	)
}

// TestBuildEndpoints asserts that creating a git app produces a build, and the
// list/detail endpoints return it (org-authorized).
func TestBuildEndpoints(t *testing.T) {
	fb := build.NewFakeBuilder()
	fb.ImageOverride = "ghcr.io/acme/built:sha"
	s := newBuildServer(t, fb)

	token := signup(t, s, "build-owner@example.com")
	org := firstOrgID(t, s, token)

	rec := doJSON(t, s, http.MethodPost, "/v1/orgs/"+org+"/apps",
		`{"name":"web","gitRepository":"https://github.com/acme/web.git","gitBranch":"main"}`, token)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create git app: %d %s", rec.Code, rec.Body.String())
	}
	var app struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&app)
	if app.Status != "building" {
		t.Fatalf("git app status = %q, want building", app.Status)
	}

	// Drain the async build before asserting the build record.
	s.WaitBuilds()

	// List builds.
	rec = doJSON(t, s, http.MethodGet, "/v1/orgs/"+org+"/apps/"+app.ID+"/builds", "", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("list builds: %d %s", rec.Code, rec.Body.String())
	}
	var list struct {
		Data []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Image  string `json:"image"`
		} `json:"data"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&list)
	if len(list.Data) != 1 {
		t.Fatalf("builds = %d, want 1", len(list.Data))
	}
	if list.Data[0].Status != "succeeded" || list.Data[0].Image != "ghcr.io/acme/built:sha" {
		t.Fatalf("unexpected build: %+v", list.Data[0])
	}

	// Build detail (includes logs field).
	buildID := list.Data[0].ID
	rec = doJSON(t, s, http.MethodGet, "/v1/orgs/"+org+"/apps/"+app.ID+"/builds/"+buildID, "", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("get build: %d %s", rec.Code, rec.Body.String())
	}
	var detail struct {
		ID    string `json:"id"`
		AppID string `json:"appId"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&detail)
	if detail.ID != buildID || detail.AppID != app.ID {
		t.Fatalf("unexpected build detail: %+v", detail)
	}

	// Unknown build id is 404.
	rec = doJSON(t, s, http.MethodGet, "/v1/orgs/"+org+"/apps/"+app.ID+"/builds/nope", "", token)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown build = %d, want 404", rec.Code)
	}
}
