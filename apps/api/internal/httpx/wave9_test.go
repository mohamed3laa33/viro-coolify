package httpx

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestReleasesRollbackScaleEndpoints exercises the Wave 9 HTTP surface: a deploy
// records a release, PATCH updates the spec and records another, GET /releases lists
// them desc, POST /scale persists bounds, and POST /rollback rolls back to a prior
// revision.
func TestReleasesRollbackScaleEndpoints(t *testing.T) {
	s := newTestServer(t, "http://unused")
	token := signup(t, s, "owner-w9@example.com")
	org := firstOrgID(t, s, token)
	base := "/v1/orgs/" + org + "/apps"

	rec := doJSON(t, s, http.MethodPost, base, `{"name":"web","image":"nginx:1"}`, token)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create app: %d %s", rec.Code, rec.Body.String())
	}
	var app struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&app)

	// PATCH the image -> a re-Apply + a new release.
	rec = doJSON(t, s, http.MethodPatch, base+"/"+app.ID, `{"image":"nginx:2"}`, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch app: %d %s", rec.Code, rec.Body.String())
	}

	// List releases (newest first).
	rec = doJSON(t, s, http.MethodGet, base+"/"+app.ID+"/releases", "", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("list releases: %d %s", rec.Code, rec.Body.String())
	}
	var rels struct {
		Data []struct {
			Revision int    `json:"revision"`
			Image    string `json:"image"`
			Status   string `json:"status"`
		} `json:"data"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&rels)
	if len(rels.Data) < 2 {
		t.Fatalf("releases = %d, want >=2", len(rels.Data))
	}
	if rels.Data[0].Revision <= rels.Data[1].Revision {
		t.Fatalf("releases not desc: %+v", rels.Data)
	}
	if rels.Data[0].Image != "nginx:2" || rels.Data[0].Status != "active" {
		t.Fatalf("top release = %+v, want active nginx:2", rels.Data[0])
	}

	// Scale bounds: scale-to-zero stateless app.
	rec = doJSON(t, s, http.MethodPost, base+"/"+app.ID+"/scale", `{"minReplicas":0,"maxReplicas":4}`, token)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("scale: %d %s", rec.Code, rec.Body.String())
	}
	var scaled struct {
		MinReplicas int `json:"minReplicas"`
		MaxReplicas int `json:"maxReplicas"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&scaled)
	if scaled.MinReplicas != 0 || scaled.MaxReplicas != 4 {
		t.Fatalf("scaled bounds = %+v, want 0/4", scaled)
	}

	// Rollback to revision 1 (the original nginx:1).
	rec = doJSON(t, s, http.MethodPost, base+"/"+app.ID+"/rollback", `{"revision":1}`, token)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("rollback: %d %s", rec.Code, rec.Body.String())
	}
	var rolled struct {
		Image string `json:"image"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&rolled)
	if rolled.Image != "nginx:1" {
		t.Fatalf("rolled-back image = %q, want nginx:1", rolled.Image)
	}

	// The app detail keeps the app fields TOP-LEVEL (backward-compatible) and adds
	// currentRelease as a sibling. The app id and image must read at the top level.
	rec = doJSON(t, s, http.MethodGet, base+"/"+app.ID, "", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("get app: %d", rec.Code)
	}
	var detail struct {
		ID             string `json:"id"`
		Image          string `json:"image"`
		CurrentRelease struct {
			Image string `json:"image"`
		} `json:"currentRelease"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&detail)
	if detail.ID != app.ID {
		t.Fatalf("app detail top-level id = %q, want %q", detail.ID, app.ID)
	}
	if detail.Image != "nginx:1" || detail.CurrentRelease.Image != "nginx:1" {
		t.Fatalf("app detail = %+v, want nginx:1 top-level + currentRelease", detail)
	}
}
