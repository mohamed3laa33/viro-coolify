package httpx

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/config"
)

// newAdminTestServer builds a Server whose admin list includes the given emails.
func newAdminTestServer(t *testing.T, adminEmails ...string) *Server {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	cfg.CoolifyBaseURL = "http://unused"
	cfg.CoolifyToken = ""
	cfg.AdminEmails = adminEmails
	return NewServer(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestAdminRoutesRequireAdmin(t *testing.T) {
	s := newAdminTestServer(t, "boss@example.com")

	// Unauthenticated -> 401.
	if rec := doJSON(t, s, http.MethodGet, "/v1/admin/plans", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anon admin plans = %d, want 401", rec.Code)
	}

	// Authenticated non-admin -> 403.
	member := signup(t, s, "peon@example.com")
	if rec := doJSON(t, s, http.MethodGet, "/v1/admin/plans", "", member); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin admin plans = %d, want 403", rec.Code)
	}
}

func TestMeExposesIsAdmin(t *testing.T) {
	s := newAdminTestServer(t, "boss@example.com")
	admin := signup(t, s, "boss@example.com")

	rec := doJSON(t, s, http.MethodGet, "/v1/me", "", admin)
	var me userView
	_ = json.NewDecoder(rec.Body).Decode(&me)
	if !me.IsAdmin {
		t.Fatalf("expected admin user, got %+v", me)
	}

	member := signup(t, s, "peon@example.com")
	rec = doJSON(t, s, http.MethodGet, "/v1/me", "", member)
	_ = json.NewDecoder(rec.Body).Decode(&me)
	if me.IsAdmin {
		t.Fatalf("expected non-admin user, got %+v", me)
	}
}

func TestAdminPlanCRUDReflectedInPublicCatalog(t *testing.T) {
	s := newAdminTestServer(t, "boss@example.com")
	admin := signup(t, s, "boss@example.com")

	// Create a new active plan.
	body := `{"id":"enterprise","name":"Enterprise","priceCents":29900,"currency":"usd","maxCpu":8,"maxMemoryMb":16384,"maxApps":1000,"active":true,"sortOrder":4}`
	if rec := doJSON(t, s, http.MethodPost, "/v1/admin/plans", body, admin); rec.Code != http.StatusCreated {
		t.Fatalf("create plan = %d %s", rec.Code, rec.Body.String())
	}

	// It shows up in the public catalog.
	rec := doJSON(t, s, http.MethodGet, "/v1/billing/plans", "", "")
	var resp struct {
		Data []struct {
			ID     string `json:"id"`
			Active bool   `json:"active"`
		} `json:"data"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	found := false
	for _, p := range resp.Data {
		if p.ID == "enterprise" {
			found = true
		}
	}
	if !found {
		t.Fatalf("enterprise plan not in public catalog: %+v", resp.Data)
	}

	// Patch it to inactive -> drops out of the public (active-only) catalog.
	if rec := doJSON(t, s, http.MethodPatch, "/v1/admin/plans/enterprise", `{"active":false}`, admin); rec.Code != http.StatusOK {
		t.Fatalf("patch plan = %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodGet, "/v1/billing/plans", "", "")
	resp.Data = nil
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	for _, p := range resp.Data {
		if p.ID == "enterprise" {
			t.Fatalf("inactive plan should not appear in public catalog")
		}
	}

	// Delete it.
	if rec := doJSON(t, s, http.MethodDelete, "/v1/admin/plans/enterprise", "", admin); rec.Code != http.StatusNoContent {
		t.Fatalf("delete plan = %d", rec.Code)
	}
	if rec := doJSON(t, s, http.MethodDelete, "/v1/admin/plans/enterprise", "", admin); rec.Code != http.StatusNotFound {
		t.Fatalf("delete missing plan = %d, want 404", rec.Code)
	}
}

func TestAdminSettingsGetAndPatch(t *testing.T) {
	s := newAdminTestServer(t, "boss@example.com")
	admin := signup(t, s, "boss@example.com")

	rec := doJSON(t, s, http.MethodGet, "/v1/admin/settings", "", admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("get settings = %d", rec.Code)
	}
	var set struct {
		DefaultCPU    float64 `json:"defaultCpu"`
		DefaultPlanID string  `json:"defaultPlanId"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&set)
	if set.DefaultPlanID != "hobby" || set.DefaultCPU != 0.25 {
		t.Fatalf("unexpected seeded settings: %+v", set)
	}

	rec = doJSON(t, s, http.MethodPatch, "/v1/admin/settings", `{"defaultCpu":0.5}`, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch settings = %d %s", rec.Code, rec.Body.String())
	}
	_ = json.NewDecoder(rec.Body).Decode(&set)
	if set.DefaultCPU != 0.5 || set.DefaultPlanID != "hobby" {
		t.Fatalf("patch did not preserve/override correctly: %+v", set)
	}
}

func TestAdminTemplateCRUDReflectedInCatalog(t *testing.T) {
	s := newAdminTestServer(t, "boss@example.com")
	admin := signup(t, s, "boss@example.com")

	body := `{"key":"supabase","name":"Supabase","description":"Backend platform.","category":"App","kind":"service","active":true,"sortOrder":11}`
	if rec := doJSON(t, s, http.MethodPost, "/v1/admin/templates", body, admin); rec.Code != http.StatusCreated {
		t.Fatalf("create template = %d %s", rec.Code, rec.Body.String())
	}

	rec := doJSON(t, s, http.MethodGet, "/v1/services/catalog", "", "")
	var resp struct {
		Data []struct {
			Key string `json:"key"`
		} `json:"data"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	found := false
	for _, tpl := range resp.Data {
		if tpl.Key == "supabase" {
			found = true
		}
	}
	if !found {
		t.Fatalf("supabase template not in public catalog: %+v", resp.Data)
	}

	// Deactivate -> drops out of the public catalog.
	if rec := doJSON(t, s, http.MethodPatch, "/v1/admin/templates/supabase", `{"active":false}`, admin); rec.Code != http.StatusOK {
		t.Fatalf("patch template = %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodGet, "/v1/services/catalog", "", "")
	resp.Data = nil
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	for _, tpl := range resp.Data {
		if tpl.Key == "supabase" {
			t.Fatalf("inactive template should not appear in public catalog")
		}
	}

	if rec := doJSON(t, s, http.MethodDelete, "/v1/admin/templates/supabase", "", admin); rec.Code != http.StatusNoContent {
		t.Fatalf("delete template = %d", rec.Code)
	}
}

func TestAdminOverview(t *testing.T) {
	s := newAdminTestServer(t, "boss@example.com")
	admin := signup(t, s, "boss@example.com")
	owner := signup(t, s, "ov-owner@example.com")
	org := firstOrgID(t, s, owner)

	if rec := doJSON(t, s, http.MethodPost, "/v1/orgs/"+org+"/billing/subscribe", `{"planId":"launch"}`, owner); rec.Code != http.StatusOK {
		t.Fatalf("subscribe = %d %s", rec.Code, rec.Body.String())
	}

	rec := doJSON(t, s, http.MethodGet, "/v1/admin/overview", "", admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("overview = %d %s", rec.Code, rec.Body.String())
	}
	var ov struct {
		OrgCount            int            `json:"orgCount"`
		UserCount           int            `json:"userCount"`
		SubscriptionsByPlan map[string]int `json:"subscriptionsByPlan"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&ov)
	if ov.UserCount < 2 || ov.OrgCount < 2 {
		t.Fatalf("unexpected overview counts: %+v", ov)
	}
	if ov.SubscriptionsByPlan["launch"] != 1 {
		t.Fatalf("expected 1 launch subscription, got %+v", ov.SubscriptionsByPlan)
	}
}
