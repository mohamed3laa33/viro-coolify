package httpx

import (
	"encoding/json"
	"net/http"
	"testing"
)

// defaultProjectID returns the id of the org's default project.
func defaultProjectID(t *testing.T, s *Server, token, org string) string {
	t.Helper()
	rec := doJSON(t, s, http.MethodGet, "/v1/orgs/"+org+"/projects/", "", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("list projects: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data []struct {
			ID        string `json:"id"`
			IsDefault bool   `json:"isDefault"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode projects: %v", err)
	}
	for _, p := range resp.Data {
		if p.IsDefault {
			return p.ID
		}
	}
	if len(resp.Data) > 0 {
		return resp.Data[0].ID
	}
	t.Fatal("no project found")
	return ""
}

func TestServiceCatalogPublic(t *testing.T) {
	s := newTestServer(t, "http://unused")
	rec := doJSON(t, s, http.MethodGet, "/v1/services/catalog", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("catalog: %d", rec.Code)
	}
	var resp struct {
		Data []struct {
			Key  string `json:"key"`
			Kind string `json:"kind"`
		} `json:"data"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp.Data) == 0 {
		t.Fatal("expected catalog templates")
	}
}

func TestQuotaOverLimitReturns402(t *testing.T) {
	s := newTestServer(t, "http://unused")
	token := signup(t, s, "quota-owner@example.com")
	org := firstOrgID(t, s, token)

	// Hobby plan default: maxCPU 0.5. Requesting 1.0 vCPU is over quota -> 402.
	rec := doJSON(t, s, http.MethodPost, "/v1/orgs/"+org+"/apps", `{"name":"big","cpu":1.0}`, token)
	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("over-quota create = %d, want 402: %s", rec.Code, rec.Body.String())
	}
}

func TestServiceCreateAndLifecycle(t *testing.T) {
	s := newTestServer(t, "http://unused")
	token := signup(t, s, "svc-owner@example.com")
	org := firstOrgID(t, s, token)
	proj := defaultProjectID(t, s, token, org)

	rec := doJSON(t, s, http.MethodPost, "/v1/orgs/"+org+"/projects/"+proj+"/services",
		`{"templateKey":"wordpress","name":"blog"}`, token)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create service: %d %s", rec.Code, rec.Body.String())
	}
	var svc struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&svc)
	if svc.ID == "" || svc.Status != "created" {
		t.Fatalf("unexpected service: %+v", svc)
	}

	rec = doJSON(t, s, http.MethodGet, "/v1/orgs/"+org+"/services/", "", token)
	var list struct {
		Data []json.RawMessage `json:"data"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&list)
	if len(list.Data) != 1 {
		t.Fatalf("expected 1 service, got %d", len(list.Data))
	}

	rec = doJSON(t, s, http.MethodPost, "/v1/orgs/"+org+"/services/"+svc.ID+"/deploy", "", token)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("deploy: %d %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodDelete, "/v1/orgs/"+org+"/services/"+svc.ID, "", token)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", rec.Code)
	}
}

func TestAppEnvEndpoints(t *testing.T) {
	s := newTestServer(t, "http://unused")
	token := signup(t, s, "env-owner@example.com")
	org := firstOrgID(t, s, token)

	rec := doJSON(t, s, http.MethodPost, "/v1/orgs/"+org+"/apps", `{"name":"web"}`, token)
	var app struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&app)

	base := "/v1/orgs/" + org + "/apps/" + app.ID + "/env"
	if rec := doJSON(t, s, http.MethodPut, base, `{"key":"FOO","value":"bar"}`, token); rec.Code != http.StatusOK {
		t.Fatalf("set env = %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodGet, base, "", token)
	var env struct {
		Data []struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		} `json:"data"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&env)
	if len(env.Data) != 1 || env.Data[0].Key != "FOO" || env.Data[0].Value != "bar" {
		t.Fatalf("unexpected env: %+v", env.Data)
	}
	if rec := doJSON(t, s, http.MethodDelete, base+"/FOO", "", token); rec.Code != http.StatusNoContent {
		t.Fatalf("delete env = %d", rec.Code)
	}
	rec = doJSON(t, s, http.MethodGet, base, "", token)
	_ = json.NewDecoder(rec.Body).Decode(&env)
	if len(env.Data) != 0 {
		t.Fatalf("expected empty env, got %+v", env.Data)
	}
}

func TestAppDomainEndpoints(t *testing.T) {
	s := newTestServer(t, "http://unused")
	token := signup(t, s, "dom-owner@example.com")
	org := firstOrgID(t, s, token)

	rec := doJSON(t, s, http.MethodPost, "/v1/orgs/"+org+"/apps", `{"name":"web"}`, token)
	var app struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&app)

	base := "/v1/orgs/" + org + "/apps/" + app.ID + "/domains"
	rec = doJSON(t, s, http.MethodPost, base, `{"domain":"example.com"}`, token)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add domain = %d %s", rec.Code, rec.Body.String())
	}
	var d struct {
		ID       string `json:"id"`
		Domain   string `json:"domain"`
		Verified bool   `json:"verified"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&d)
	if d.ID == "" || d.Domain != "example.com" {
		t.Fatalf("unexpected domain: %+v", d)
	}

	rec = doJSON(t, s, http.MethodGet, base, "", token)
	var list struct {
		Data []json.RawMessage `json:"data"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&list)
	if len(list.Data) != 1 {
		t.Fatalf("expected 1 domain, got %d", len(list.Data))
	}

	if rec := doJSON(t, s, http.MethodDelete, base+"/"+d.ID, "", token); rec.Code != http.StatusNoContent {
		t.Fatalf("delete domain = %d", rec.Code)
	}
}

func TestAppMetricsEndpoint(t *testing.T) {
	s := newTestServer(t, "http://unused")
	token := signup(t, s, "metrics-owner@example.com")
	org := firstOrgID(t, s, token)

	rec := doJSON(t, s, http.MethodPost, "/v1/orgs/"+org+"/apps", `{"name":"web"}`, token)
	var app struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&app)

	rec = doJSON(t, s, http.MethodGet, "/v1/orgs/"+org+"/apps/"+app.ID+"/metrics", "", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics = %d %s", rec.Code, rec.Body.String())
	}
	var m struct {
		CPU      []map[string]float64 `json:"cpu"`
		Memory   []map[string]float64 `json:"memory"`
		Requests []map[string]float64 `json:"requests"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&m)
	if len(m.CPU) != 24 || len(m.Memory) != 24 || len(m.Requests) != 24 {
		t.Fatalf("metrics shape: cpu=%d mem=%d req=%d", len(m.CPU), len(m.Memory), len(m.Requests))
	}
}
