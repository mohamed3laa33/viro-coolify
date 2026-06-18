package httpx

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
)

// userIDFromToken resolves the user id for a signed-up token via /v1/me.
func userIDFromToken(t *testing.T, s *Server, token string) string {
	t.Helper()
	rec := doJSON(t, s, http.MethodGet, "/v1/me", "", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("me: %d %s", rec.Code, rec.Body.String())
	}
	var u userView
	if err := json.NewDecoder(rec.Body).Decode(&u); err != nil {
		t.Fatalf("decode me: %v", err)
	}
	return u.ID
}

// createAppViaAPI creates an app in the org's default project (owner is admin+).
func createAppViaAPI(t *testing.T, s *Server, token, orgID string) domain.App {
	t.Helper()
	rec := doJSON(t, s, http.MethodPost, "/v1/orgs/"+orgID+"/apps",
		`{"name":"web","image":"nginx:1.27"}`, token)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create app: %d %s", rec.Code, rec.Body.String())
	}
	var a domain.App
	if err := json.NewDecoder(rec.Body).Decode(&a); err != nil {
		t.Fatalf("decode app: %v", err)
	}
	return a
}

func TestProjectMembershipAuthzIDOR(t *testing.T) {
	s := newTestServer(t, "http://unused")
	ctx := context.Background()

	owner := signup(t, s, "owner@example.com")
	orgID := firstOrgID(t, s, owner)
	app := createAppViaAPI(t, s, owner, orgID)

	// A second user joins the org as a PLAIN MEMBER (not a project member).
	outsider := signup(t, s, "outsider@example.com")
	outsiderID := userIDFromToken(t, s, outsider)
	if err := s.store.AddMembership(ctx, domain.Membership{OrgID: orgID, UserID: outsiderID, Role: domain.RoleMember}); err != nil {
		t.Fatalf("add membership: %v", err)
	}

	// The org member who is NOT a project member must be 403'd on the app.
	if rec := doJSON(t, s, http.MethodGet, "/v1/orgs/"+orgID+"/apps/"+app.ID, "", outsider); rec.Code != http.StatusForbidden {
		t.Fatalf("outsider GET app = %d, want 403", rec.Code)
	}
	if rec := doJSON(t, s, http.MethodPost, "/v1/orgs/"+orgID+"/apps/"+app.ID+"/deploy", "", outsider); rec.Code != http.StatusForbidden {
		t.Fatalf("outsider deploy = %d, want 403", rec.Code)
	}
	if rec := doJSON(t, s, http.MethodDelete, "/v1/orgs/"+orgID+"/apps/"+app.ID, "", outsider); rec.Code != http.StatusForbidden {
		t.Fatalf("outsider delete = %d, want 403", rec.Code)
	}

	// The org OWNER (admin+) retains full access.
	if rec := doJSON(t, s, http.MethodGet, "/v1/orgs/"+orgID+"/apps/"+app.ID, "", owner); rec.Code != http.StatusOK {
		t.Fatalf("owner GET app = %d, want 200", rec.Code)
	}

	// Granting the outsider PROJECT membership lets them in.
	if err := s.store.AddProjectMembership(ctx, domain.ProjectMembership{ProjectID: app.ProjectID, UserID: outsiderID, Role: domain.RoleMember}); err != nil {
		t.Fatalf("add project membership: %v", err)
	}
	if rec := doJSON(t, s, http.MethodGet, "/v1/orgs/"+orgID+"/apps/"+app.ID, "", outsider); rec.Code != http.StatusOK {
		t.Fatalf("project member GET app = %d, want 200", rec.Code)
	}
	// But a plain member still can't perform admin ops (deploy needs RoleAdmin).
	if rec := doJSON(t, s, http.MethodPost, "/v1/orgs/"+orgID+"/apps/"+app.ID+"/deploy", "", outsider); rec.Code != http.StatusForbidden {
		t.Fatalf("project member deploy = %d, want 403 (needs admin)", rec.Code)
	}
}

func TestListAppsScopedToAccessibleProjects(t *testing.T) {
	s := newTestServer(t, "http://unused")
	ctx := context.Background()
	owner := signup(t, s, "owner2@example.com")
	orgID := firstOrgID(t, s, owner)
	createAppViaAPI(t, s, owner, orgID)

	outsider := signup(t, s, "outsider2@example.com")
	outsiderID := userIDFromToken(t, s, outsider)
	_ = s.store.AddMembership(ctx, domain.Membership{OrgID: orgID, UserID: outsiderID, Role: domain.RoleMember})

	// A plain org member with no project access sees an EMPTY app list.
	rec := doJSON(t, s, http.MethodGet, "/v1/orgs/"+orgID+"/apps", "", outsider)
	if rec.Code != http.StatusOK {
		t.Fatalf("list apps = %d", rec.Code)
	}
	var resp struct {
		Data []domain.App `json:"data"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp.Data) != 0 {
		t.Fatalf("expected empty list for non-project-member, got %d", len(resp.Data))
	}
}

func TestEnvSecretMaskingAndAdminRequired(t *testing.T) {
	s := newTestServer(t, "http://unused")
	ctx := context.Background()
	owner := signup(t, s, "owner3@example.com")
	orgID := firstOrgID(t, s, owner)
	app := createAppViaAPI(t, s, owner, orgID)

	// Set a secret via the API.
	rec := doJSON(t, s, http.MethodPut, "/v1/orgs/"+orgID+"/apps/"+app.ID+"/env",
		`{"key":"API_KEY","value":"s3cret","secret":true}`, owner)
	if rec.Code != http.StatusOK {
		t.Fatalf("set secret env = %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "s3cret") {
		t.Fatalf("SetEnv response leaked secret value: %s", rec.Body.String())
	}

	// GET env masks the secret and never returns its value.
	rec = doJSON(t, s, http.MethodGet, "/v1/orgs/"+orgID+"/apps/"+app.ID+"/env", "", owner)
	if rec.Code != http.StatusOK {
		t.Fatalf("list env = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "s3cret") {
		t.Fatalf("ListEnv leaked secret value: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "***") {
		t.Fatalf("ListEnv did not mask secret: %s", rec.Body.String())
	}

	// A plain project member (RoleMember) cannot list env (admin required).
	member := signup(t, s, "envmember@example.com")
	memberID := userIDFromToken(t, s, member)
	_ = s.store.AddMembership(ctx, domain.Membership{OrgID: orgID, UserID: memberID, Role: domain.RoleMember})
	_ = s.store.AddProjectMembership(ctx, domain.ProjectMembership{ProjectID: app.ProjectID, UserID: memberID, Role: domain.RoleMember})
	if rec := doJSON(t, s, http.MethodGet, "/v1/orgs/"+orgID+"/apps/"+app.ID+"/env", "", member); rec.Code != http.StatusForbidden {
		t.Fatalf("member list env = %d, want 403 (admin required)", rec.Code)
	}
}

func TestAuditRecordedForSecretWriteWithoutValue(t *testing.T) {
	s := newTestServer(t, "http://unused")
	ctx := context.Background()
	owner := signup(t, s, "owner4@example.com")
	orgID := firstOrgID(t, s, owner)
	app := createAppViaAPI(t, s, owner, orgID)

	rec := doJSON(t, s, http.MethodPut, "/v1/orgs/"+orgID+"/apps/"+app.ID+"/env",
		`{"key":"API_KEY","value":"top-secret","secret":true}`, owner)
	if rec.Code != http.StatusOK {
		t.Fatalf("set secret = %d %s", rec.Code, rec.Body.String())
	}

	events, err := s.store.ListAuditEvents(ctx, domain.AuditFilter{OrgID: orgID, Limit: 50})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	var found bool
	for _, e := range events {
		if e.Action == "secret.set" {
			found = true
			if !strings.Contains(e.TargetID, "API_KEY") {
				t.Fatalf("audit target should reference the key: %+v", e)
			}
			if strings.Contains(e.TargetID, "top-secret") || strings.Contains(e.Metadata, "top-secret") {
				t.Fatalf("AUDIT LEAKED SECRET VALUE: %+v", e)
			}
			if e.ActorEmail != "owner4@example.com" {
				t.Fatalf("audit actor = %q", e.ActorEmail)
			}
		}
	}
	if !found {
		t.Fatalf("no secret.set audit event recorded; have %+v", events)
	}
}

func TestAdminMutationsAudited(t *testing.T) {
	s := newAdminTestServer(t, "boss@example.com")
	ctx := context.Background()
	admin := signup(t, s, "boss@example.com")

	// Create a plan via the admin API.
	rec := doJSON(t, s, http.MethodPost, "/v1/admin/plans",
		`{"id":"pro","name":"Pro","priceCents":1000,"currency":"usd"}`, admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create plan = %d %s", rec.Code, rec.Body.String())
	}

	events, _ := s.store.ListAuditEvents(ctx, domain.AuditFilter{OrgID: "", Limit: 50})
	var found bool
	for _, e := range events {
		if e.Action == "plan.create" && e.TargetID == "pro" {
			found = true
			if e.ActorEmail != "boss@example.com" {
				t.Fatalf("plan.create actor = %q", e.ActorEmail)
			}
		}
	}
	if !found {
		t.Fatalf("plan.create not audited; have %+v", events)
	}

	// The platform audit endpoint returns events to the super-admin.
	rec = doJSON(t, s, http.MethodGet, "/v1/admin/audit", "", admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin audit = %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "plan.create") {
		t.Fatalf("admin audit listing missing plan.create: %s", rec.Body.String())
	}
}

func TestAuthLoginAudited(t *testing.T) {
	s := newTestServer(t, "http://unused")
	ctx := context.Background()
	_ = signup(t, s, "loginuser@example.com")

	// Successful login.
	if rec := doJSON(t, s, http.MethodPost, "/v1/auth/login",
		`{"email":"loginuser@example.com","password":"supersecret"}`, ""); rec.Code != http.StatusOK {
		t.Fatalf("login = %d %s", rec.Code, rec.Body.String())
	}
	// Failed login.
	if rec := doJSON(t, s, http.MethodPost, "/v1/auth/login",
		`{"email":"loginuser@example.com","password":"wrongpass"}`, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad login = %d", rec.Code)
	}

	events, _ := s.store.ListAuditEvents(ctx, domain.AuditFilter{OrgID: "", Limit: 50})
	var ok, failed bool
	for _, e := range events {
		switch e.Action {
		case "auth.login":
			ok = true
		case "auth.login_failed":
			failed = true
			if e.ActorEmail != "loginuser@example.com" {
				t.Fatalf("login_failed email = %q", e.ActorEmail)
			}
		}
	}
	if !ok || !failed {
		t.Fatalf("auth audit missing (login=%v failed=%v): %+v", ok, failed, events)
	}
}
