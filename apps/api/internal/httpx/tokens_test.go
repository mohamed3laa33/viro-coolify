package httpx

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
)

// signupUser registers a user and returns the access token from the response.
func signupUser(t *testing.T, s *Server, email string) string {
	t.Helper()
	rec := doJSON(t, s, http.MethodPost, "/v1/auth/signup",
		`{"email":"`+email+`","name":"User","password":"supersecret"}`, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("signup status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var auth authResponse
	if err := json.NewDecoder(rec.Body).Decode(&auth); err != nil {
		t.Fatalf("decode signup: %v", err)
	}
	if auth.AccessToken == "" {
		t.Fatalf("signup returned no access token")
	}
	return auth.AccessToken
}

func TestCreateTokenReturnsOneTimeSecretAndPersistsOnlyHash(t *testing.T) {
	s := newTestServer(t, "http://unused")
	access := signupUser(t, s, "alice@example.com")

	rec := doJSON(t, s, http.MethodPost, "/v1/tokens", `{"name":"ci","scopes":["read"]}`, access)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create token = %d, body = %s", rec.Code, rec.Body.String())
	}
	var created createTokenView
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if !strings.HasPrefix(created.Token, "vrt_") {
		t.Fatalf("token = %q, want vrt_ prefix", created.Token)
	}
	if created.Prefix != created.Token[:8] {
		t.Fatalf("prefix = %q, want %q", created.Prefix, created.Token[:8])
	}
	if created.ID == "" || created.Name != "ci" {
		t.Fatalf("unexpected created token: %+v", created)
	}
	if len(created.Scopes) != 1 || created.Scopes[0] != "read" {
		t.Fatalf("scopes = %v, want [read]", created.Scopes)
	}

	// The store must persist ONLY the hash, never the plaintext.
	tok, err := s.store.GetApiTokenByHash(context.Background(), hashApiToken(created.Token))
	if err != nil {
		t.Fatalf("lookup by hash: %v", err)
	}
	if tok.TokenHash == created.Token {
		t.Fatalf("stored token hash equals plaintext")
	}
	if tok.TokenHash != hashApiToken(created.Token) {
		t.Fatalf("stored hash mismatch")
	}
}

func TestPATAuthenticatesAsOwner(t *testing.T) {
	s := newTestServer(t, "http://unused")
	access := signupUser(t, s, "bob@example.com")

	rec := doJSON(t, s, http.MethodPost, "/v1/tokens", `{"name":"cli"}`, access)
	var created createTokenView
	_ = json.NewDecoder(rec.Body).Decode(&created)

	// Use the PAT (not the JWT) to hit /me; it must resolve to the owner.
	rec = doJSON(t, s, http.MethodGet, "/v1/me", "", created.Token)
	if rec.Code != http.StatusOK {
		t.Fatalf("me with PAT = %d, body = %s", rec.Code, rec.Body.String())
	}
	var me userView
	if err := json.NewDecoder(rec.Body).Decode(&me); err != nil {
		t.Fatalf("decode me: %v", err)
	}
	if me.Email != "bob@example.com" {
		t.Fatalf("me email = %q, want bob@example.com", me.Email)
	}
}

func TestListTokensNeverLeaksSecret(t *testing.T) {
	s := newTestServer(t, "http://unused")
	access := signupUser(t, s, "carol@example.com")

	rec := doJSON(t, s, http.MethodPost, "/v1/tokens", `{"name":"t1"}`, access)
	var created createTokenView
	_ = json.NewDecoder(rec.Body).Decode(&created)

	rec = doJSON(t, s, http.MethodGet, "/v1/tokens", "", access)
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, created.Token) {
		t.Fatalf("list leaked the plaintext token: %s", body)
	}
	if strings.Contains(body, "\"token\"") {
		t.Fatalf("list response contains a token field: %s", body)
	}
	var resp struct {
		Data []tokenView `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(resp.Data) != 1 || resp.Data[0].ID != created.ID || resp.Data[0].Prefix != created.Prefix {
		t.Fatalf("unexpected list data: %+v", resp.Data)
	}
}

func TestExpiredTokenRejected(t *testing.T) {
	s := newTestServer(t, "http://unused")
	access := signupUser(t, s, "dan@example.com")
	// Resolve the owning user so we can plant an already-expired token directly.
	rec := doJSON(t, s, http.MethodGet, "/v1/me", "", access)
	var me userView
	_ = json.NewDecoder(rec.Body).Decode(&me)

	plaintext := "vrt_expiredtokenfixture"
	if err := s.store.CreateApiToken(context.Background(), &domain.ApiToken{
		ID:        "tok-expired",
		UserID:    me.ID,
		Name:      "old",
		TokenHash: hashApiToken(plaintext),
		Prefix:    plaintext[:8],
		ExpiresAt: time.Now().Add(-time.Hour),
		CreatedAt: time.Now().Add(-2 * time.Hour),
	}); err != nil {
		t.Fatalf("seed expired token: %v", err)
	}

	if rec := doJSON(t, s, http.MethodGet, "/v1/me", "", plaintext); rec.Code != http.StatusUnauthorized {
		t.Fatalf("expired PAT = %d, want 401", rec.Code)
	}
}

func TestDeletedTokenRejected(t *testing.T) {
	s := newTestServer(t, "http://unused")
	access := signupUser(t, s, "erin@example.com")

	rec := doJSON(t, s, http.MethodPost, "/v1/tokens", `{"name":"todelete"}`, access)
	var created createTokenView
	_ = json.NewDecoder(rec.Body).Decode(&created)

	// PAT works before delete.
	if rec := doJSON(t, s, http.MethodGet, "/v1/me", "", created.Token); rec.Code != http.StatusOK {
		t.Fatalf("pre-delete PAT = %d, want 200", rec.Code)
	}

	if rec := doJSON(t, s, http.MethodDelete, "/v1/tokens/"+created.ID, "", access); rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d, want 204", rec.Code)
	}

	// PAT is now revoked -> 401.
	if rec := doJSON(t, s, http.MethodGet, "/v1/me", "", created.Token); rec.Code != http.StatusUnauthorized {
		t.Fatalf("post-delete PAT = %d, want 401", rec.Code)
	}

	// Deleting again -> 404.
	if rec := doJSON(t, s, http.MethodDelete, "/v1/tokens/"+created.ID, "", access); rec.Code != http.StatusNotFound {
		t.Fatalf("re-delete = %d, want 404", rec.Code)
	}
}

func TestDeleteTokenScopedToOwner(t *testing.T) {
	s := newTestServer(t, "http://unused")
	aliceAccess := signupUser(t, s, "owner@example.com")
	malloryAccess := signupUser(t, s, "mallory@example.com")

	rec := doJSON(t, s, http.MethodPost, "/v1/tokens", `{"name":"victim"}`, aliceAccess)
	var created createTokenView
	_ = json.NewDecoder(rec.Body).Decode(&created)

	// Mallory cannot delete Alice's token (scoped to owner -> 404).
	if rec := doJSON(t, s, http.MethodDelete, "/v1/tokens/"+created.ID, "", malloryAccess); rec.Code != http.StatusNotFound {
		t.Fatalf("cross-user delete = %d, want 404", rec.Code)
	}
	// Alice's token still authenticates.
	if rec := doJSON(t, s, http.MethodGet, "/v1/me", "", created.Token); rec.Code != http.StatusOK {
		t.Fatalf("token still valid = %d, want 200", rec.Code)
	}
}

func TestPATRespectsOrgAuthz(t *testing.T) {
	s := newTestServer(t, "http://unused")
	// Owner creates an org via JWT.
	ownerAccess := signupUser(t, s, "boss@example.com")
	rec := doJSON(t, s, http.MethodPost, "/v1/orgs", `{"name":"Acme"}`, ownerAccess)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create org = %d, body = %s", rec.Code, rec.Body.String())
	}
	var org struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&org)

	// Owner's PAT can list the org's members (member+ read).
	rec = doJSON(t, s, http.MethodPost, "/v1/tokens", `{"name":"ops"}`, ownerAccess)
	var ownerTok createTokenView
	_ = json.NewDecoder(rec.Body).Decode(&ownerTok)
	if rec := doJSON(t, s, http.MethodGet, "/v1/orgs/"+org.ID+"/members", "", ownerTok.Token); rec.Code != http.StatusOK {
		t.Fatalf("owner PAT list members = %d, want 200", rec.Code)
	}

	// An outsider's PAT is forbidden from the org, exactly like the user.
	outsiderAccess := signupUser(t, s, "outsider@example.com")
	rec = doJSON(t, s, http.MethodPost, "/v1/tokens", `{"name":"outside"}`, outsiderAccess)
	var outsiderTok createTokenView
	_ = json.NewDecoder(rec.Body).Decode(&outsiderTok)
	if rec := doJSON(t, s, http.MethodGet, "/v1/orgs/"+org.ID+"/members", "", outsiderTok.Token); rec.Code == http.StatusOK {
		t.Fatalf("outsider PAT list members = %d, want non-200", rec.Code)
	}
}
