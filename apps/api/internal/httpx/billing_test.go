package httpx

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

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

// postSignedWebhook posts a Stripe-signed webhook body to the server and returns
// the response recorder.
func postSignedWebhook(t *testing.T, s *Server, secret, body string) *httptest.ResponseRecorder {
	t.Helper()
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "."))
	mac.Write([]byte(body))
	sig := "t=" + ts + ",v1=" + hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest(http.MethodPost, "/v1/billing/webhook", strings.NewReader(body))
	req.Header.Set("Stripe-Signature", sig)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	return rec
}

// TestStripeWebhookStatusMappingAndIdempotency drives the HTTP webhook end-to-end:
// it maps the event's REAL status (past_due, not forced active) and dedupes a
// redelivered event by id.
func TestStripeWebhookStatusMappingAndIdempotency(t *testing.T) {
	s := newTestServer(t, "http://unused")
	s.cfg.StripeWebhookSecret = "whsec_test"
	ctx := context.Background()
	if err := s.store.UpsertSubscription(ctx, &domain.Subscription{
		OrgID: "org-1", PlanID: "launch", Status: domain.SubActive,
		StripeCustomerID: "cus_1", StripeSubscriptionID: "sub_1",
		CurrentPeriodEnd: time.Now().AddDate(0, 1, 0),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	body := `{"id":"evt_1","type":"customer.subscription.updated","data":{"object":{"id":"sub_1","customer":"cus_1","status":"past_due","metadata":{"org_id":"org-1"}}}}`
	if rec := postSignedWebhook(t, s, "whsec_test", body); rec.Code != http.StatusOK {
		t.Fatalf("webhook = %d %s", rec.Code, rec.Body.String())
	}
	got, _ := s.store.GetSubscription(ctx, "org-1")
	if got.Status != domain.SubPastDue {
		t.Fatalf("status = %q want past_due (not forced active)", got.Status)
	}

	// Now flip it to active locally and re-deliver the SAME event id: dedupe must
	// prevent the past_due event from re-applying.
	got.Status = domain.SubActive
	_ = s.store.UpsertSubscription(ctx, got)
	if rec := postSignedWebhook(t, s, "whsec_test", body); rec.Code != http.StatusOK {
		t.Fatalf("redeliver = %d", rec.Code)
	}
	got, _ = s.store.GetSubscription(ctx, "org-1")
	if got.Status != domain.SubActive {
		t.Fatalf("redelivered event re-applied: status = %q want active (deduped)", got.Status)
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
