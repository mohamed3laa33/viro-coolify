package httpx

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
)

func TestBillingPlansArePublic(t *testing.T) {
	s := newTestServer(t, "http://unused")
	rec := doJSON(t, s, http.MethodGet, "/v1/billing/plans", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("plans = %d", rec.Code)
	}
	var resp struct {
		Data     []json.RawMessage `json:"data"`
		Provider string            `json:"provider"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp.Data) < 2 {
		t.Fatalf("expected catalog, got %d plans", len(resp.Data))
	}
	if resp.Provider != "mock" {
		t.Fatalf("provider = %q, want mock", resp.Provider)
	}
}

func TestBillingSubscribeAndSummary(t *testing.T) {
	s := newTestServer(t, "http://unused")
	owner := signup(t, s, "billing-owner@example.com")
	org := firstOrgID(t, s, owner)

	rec := doJSON(t, s, http.MethodPost, "/v1/orgs/"+org+"/billing/subscribe", `{"planId":"launch"}`, owner)
	if rec.Code != http.StatusOK {
		t.Fatalf("subscribe = %d %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodGet, "/v1/orgs/"+org+"/billing", "", owner)
	if rec.Code != http.StatusOK {
		t.Fatalf("get billing = %d", rec.Code)
	}
	var sum struct {
		Plan *struct {
			ID string `json:"id"`
		} `json:"plan"`
		Subscription *struct {
			Status string `json:"status"`
		} `json:"subscription"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&sum)
	if sum.Plan == nil || sum.Plan.ID != "launch" {
		t.Fatalf("plan = %+v", sum.Plan)
	}
	if sum.Subscription == nil || sum.Subscription.Status != "active" {
		t.Fatalf("subscription = %+v", sum.Subscription)
	}
}

func TestBillingSubscribeRequiresAdmin(t *testing.T) {
	s := newTestServer(t, "http://unused")
	owner := signup(t, s, "bill-admin@example.com")
	org := firstOrgID(t, s, owner)
	member := signup(t, s, "bill-member@example.com")

	meRec := doJSON(t, s, http.MethodGet, "/v1/me", "", member)
	var me struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(meRec.Body).Decode(&me)
	if err := s.store.AddMembership(context.Background(), domain.Membership{OrgID: org, UserID: me.ID, Role: domain.RoleMember}); err != nil {
		t.Fatalf("add membership: %v", err)
	}

	if rec := doJSON(t, s, http.MethodPost, "/v1/orgs/"+org+"/billing/subscribe", `{"planId":"launch"}`, member); rec.Code != http.StatusForbidden {
		t.Fatalf("member subscribe = %d, want 403", rec.Code)
	}
}

// TestCrossTenantAppIDIsNotFound locks in the IDOR defense: an admin of org B who
// knows org A's app id still gets 404 (not 200) when addressing it under org B.
func TestCrossTenantAppIDIsNotFound(t *testing.T) {
	s := newTestServer(t, "http://unused")
	a := signup(t, s, "idor-a@example.com")
	orgA := firstOrgID(t, s, a)
	b := signup(t, s, "idor-b@example.com")
	orgB := firstOrgID(t, s, b)

	rec := doJSON(t, s, http.MethodPost, "/v1/orgs/"+orgA+"/apps", `{"name":"secret"}`, a)
	var app struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&app)

	// B is an owner of orgB, so orgAuthz passes — but the app belongs to orgA.
	rec = doJSON(t, s, http.MethodGet, "/v1/orgs/"+orgB+"/apps/"+app.ID, "", b)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant app id = %d, want 404", rec.Code)
	}
}

func TestDatabaseMutationRequiresAdmin(t *testing.T) {
	s := newTestServer(t, "http://unused")
	owner := signup(t, s, "db-admin@example.com")
	org := firstOrgID(t, s, owner)
	member := signup(t, s, "db-member@example.com")

	meRec := doJSON(t, s, http.MethodGet, "/v1/me", "", member)
	var me struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(meRec.Body).Decode(&me)
	_ = s.store.AddMembership(context.Background(), domain.Membership{OrgID: org, UserID: me.ID, Role: domain.RoleMember})

	if rec := doJSON(t, s, http.MethodPost, "/v1/orgs/"+org+"/databases", `{"name":"db","engine":"postgresql"}`, member); rec.Code != http.StatusForbidden {
		t.Fatalf("member create db = %d, want 403", rec.Code)
	}
}
