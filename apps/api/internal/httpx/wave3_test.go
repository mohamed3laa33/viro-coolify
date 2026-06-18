package httpx

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestDatabaseConnectionInfoEndpoint asserts GET .../databases/{id} returns the
// database detail plus in-cluster connection info (host/port/connectionString),
// and that cross-tenant access is hidden as 404.
func TestDatabaseConnectionInfoEndpoint(t *testing.T) {
	s := newTestServer(t, "http://unused")
	owner := signup(t, s, "db-conn-owner@example.com")
	org := firstOrgID(t, s, owner)

	rec := doJSON(t, s, http.MethodPost, "/v1/orgs/"+org+"/databases",
		`{"name":"maindb","engine":"postgresql"}`, owner)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create db = %d %s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&created)

	got := doJSON(t, s, http.MethodGet, "/v1/orgs/"+org+"/databases/"+created.ID, "", owner)
	if got.Code != http.StatusOK {
		t.Fatalf("get db detail = %d %s", got.Code, got.Body.String())
	}
	var detail struct {
		ID         string `json:"id"`
		Connection struct {
			Host             string `json:"host"`
			Port             int    `json:"port"`
			Database         string `json:"database"`
			Username         string `json:"username"`
			Password         string `json:"password"`
			ConnectionString string `json:"connectionString"`
		} `json:"connection"`
	}
	if err := json.NewDecoder(got.Body).Decode(&detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if !strings.HasSuffix(detail.Connection.Host, ".svc.cluster.local") {
		t.Errorf("host = %q, want in-cluster DNS", detail.Connection.Host)
	}
	if detail.Connection.Port != 5432 {
		t.Errorf("port = %d, want 5432", detail.Connection.Port)
	}
	if detail.Connection.Password == "" || detail.Connection.Database == "" {
		t.Errorf("missing creds in detail: %+v", detail.Connection)
	}
	if !strings.HasPrefix(detail.Connection.ConnectionString, "postgres://") {
		t.Errorf("connectionString = %q, want postgres:// URI", detail.Connection.ConnectionString)
	}

	// Cross-tenant: another org's owner cannot see this database (404).
	other := signup(t, s, "db-conn-other@example.com")
	otherOrg := firstOrgID(t, s, other)
	if rec := doJSON(t, s, http.MethodGet, "/v1/orgs/"+otherOrg+"/databases/"+created.ID, "", other); rec.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant get db = %d, want 404", rec.Code)
	}
}

// TestListDatabasesNeverLeaksCredentials asserts the bulk list endpoint
// (GET .../databases) NEVER serializes the plaintext password/username/db-name
// (json:"-" on the bare model), while the per-database detail endpoint still
// returns them via its explicit connection-info DTO. This is the regression
// guard for the bulk-credential-leak finding.
func TestListDatabasesNeverLeaksCredentials(t *testing.T) {
	s := newTestServer(t, "http://unused")
	owner := signup(t, s, "db-leak-owner@example.com")
	org := firstOrgID(t, s, owner)

	rec := doJSON(t, s, http.MethodPost, "/v1/orgs/"+org+"/databases",
		`{"name":"secretdb","engine":"postgresql"}`, owner)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create db = %d %s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&created)

	// List: the raw JSON must contain no credential material whatsoever.
	list := doJSON(t, s, http.MethodGet, "/v1/orgs/"+org+"/databases", "", owner)
	if list.Code != http.StatusOK {
		t.Fatalf("list databases = %d %s", list.Code, list.Body.String())
	}
	body := list.Body.String()
	for _, leak := range []string{"password", "Password", `"username"`, "databaseName"} {
		if strings.Contains(body, leak) {
			t.Fatalf("list-databases response leaked %q: %s", leak, body)
		}
	}

	// The detail endpoint still returns the password (explicit connection DTO).
	got := doJSON(t, s, http.MethodGet, "/v1/orgs/"+org+"/databases/"+created.ID, "", owner)
	if got.Code != http.StatusOK {
		t.Fatalf("get detail = %d %s", got.Code, got.Body.String())
	}
	var detail struct {
		Connection struct {
			Password string `json:"password"`
		} `json:"connection"`
	}
	if err := json.NewDecoder(got.Body).Decode(&detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if detail.Connection.Password == "" {
		t.Fatalf("detail endpoint should still return the password, got empty")
	}
}

// TestDatabaseLifecycleRoutes asserts the stop/deploy routes drive the database
// and keep returning the record.
func TestDatabaseLifecycleRoutes(t *testing.T) {
	s := newTestServer(t, "http://unused")
	owner := signup(t, s, "db-life-owner@example.com")
	org := firstOrgID(t, s, owner)

	rec := doJSON(t, s, http.MethodPost, "/v1/orgs/"+org+"/databases",
		`{"name":"lifedb","engine":"postgresql"}`, owner)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create db = %d %s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID      string `json:"id"`
		Release string `json:"release"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&created)

	for _, action := range []string{"stop", "restart", "deploy"} {
		got := doJSON(t, s, http.MethodPost, "/v1/orgs/"+org+"/databases/"+created.ID+"/"+action, "", owner)
		if got.Code != http.StatusOK {
			t.Fatalf("%s db = %d %s", action, got.Code, got.Body.String())
		}
	}
}
