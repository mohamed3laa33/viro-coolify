package httpx

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/store"
)

// apiTokenPrefix is the scheme marker that distinguishes a personal access token
// from a JWT in the Authorization: Bearer header. A bearer value starting with
// this prefix is looked up by hash; anything else is treated as a JWT.
const apiTokenPrefix = "vrt_"

// apiTokenDisplayPrefixLen is how many leading characters of the plaintext token
// are stored for display (e.g. "vrt_ab12") — never enough to reconstruct the
// secret, just enough for a human to recognize a token in a listing.
const apiTokenDisplayPrefixLen = 8

// apiTokenMaxExpiryDays caps a token's requested lifetime. 0 (or unset) means the
// token never expires.
const apiTokenMaxExpiryDays = 3650

// b32 is the lowercase, unpadded base32 alphabet used to render the random token
// body — URL/clipboard-safe and case-insensitive.
var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// generateApiToken returns a fresh "vrt_<random>" token using 32 bytes of
// crypto/rand entropy, plus its SHA-256 hex hash (what is stored) and its display
// prefix. The plaintext is returned to the caller ONCE and never persisted.
func generateApiToken() (plaintext, tokenHash, prefix string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", "", err
	}
	plaintext = apiTokenPrefix + strings.ToLower(b32.EncodeToString(raw))
	tokenHash = hashApiToken(plaintext)
	prefix = plaintext
	if len(prefix) > apiTokenDisplayPrefixLen {
		prefix = prefix[:apiTokenDisplayPrefixLen]
	}
	return plaintext, tokenHash, prefix, nil
}

// hashApiToken returns the SHA-256 hex digest of the full plaintext token. This
// is the value stored and the lookup key for Bearer auth — the plaintext itself
// is never stored.
func hashApiToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// isApiToken reports whether a bearer value is a personal access token (vs a JWT),
// detected by the "vrt_" scheme prefix.
func isApiToken(bearer string) bool {
	return strings.HasPrefix(bearer, apiTokenPrefix)
}

// authViaApiToken resolves a PAT bearer value to its owning user. It hashes the
// token, looks it up, rejects a missing or expired token, loads the owner, and
// best-effort updates last-used asynchronously. A nil error means the request may
// proceed as the returned user.
func (s *Server) authViaApiToken(ctx context.Context, bearer string) (*domain.User, error) {
	tok, err := s.store.GetApiTokenByHash(ctx, hashApiToken(bearer))
	if err != nil {
		return nil, errors.New("invalid token")
	}
	if !tok.ExpiresAt.IsZero() && !time.Now().Before(tok.ExpiresAt) {
		return nil, errors.New("expired token")
	}
	user, err := s.identity.GetUser(ctx, tok.UserID)
	if err != nil {
		return nil, errors.New("unknown user")
	}
	// Best-effort last-used bump on a context DERIVED from the request but stripped
	// of its cancellation (WithoutCancel), so the write neither blocks/fails the
	// authenticated request nor is aborted when the request finishes. A short
	// timeout bounds the detached write.
	go func(id string) {
		bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := s.store.TouchApiToken(bg, id, time.Now()); err != nil {
			s.logger.Warn("api token: touch last-used", "token", id, "err", err)
		}
	}(tok.ID)
	return user, nil
}

// --- request / response shapes ---

type createTokenRequest struct {
	Name          string   `json:"name"`
	Scopes        []string `json:"scopes,omitempty"`
	ExpiresInDays int      `json:"expiresInDays,omitempty"`
}

// tokenView is the safe listing/representation of a PAT — it NEVER carries the
// secret (only the stored display prefix).
type tokenView struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Scopes     []string   `json:"scopes"`
	ExpiresAt  *time.Time `json:"expiresAt"`
	LastUsedAt *time.Time `json:"lastUsedAt"`
	CreatedAt  time.Time  `json:"createdAt"`
}

// createTokenView is the create-only response: identical to tokenView but adds the
// one-time plaintext Token field.
type createTokenView struct {
	tokenView
	Token string `json:"token"`
}

func toTokenView(t *domain.ApiToken) tokenView {
	scopes := t.Scopes
	if scopes == nil {
		scopes = []string{}
	}
	v := tokenView{
		ID:        t.ID,
		Name:      t.Name,
		Prefix:    t.Prefix,
		Scopes:    scopes,
		CreatedAt: t.CreatedAt,
	}
	if !t.ExpiresAt.IsZero() {
		e := t.ExpiresAt
		v.ExpiresAt = &e
	}
	if !t.LastUsedAt.IsZero() {
		l := t.LastUsedAt
		v.LastUsedAt = &l
	}
	return v
}

// handleCreateToken issues a new personal access token for the authenticated
// user. The plaintext token is returned ONCE in this response and never again.
func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req createTokenRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.ExpiresInDays < 0 || req.ExpiresInDays > apiTokenMaxExpiryDays {
		writeError(w, http.StatusBadRequest, "expiresInDays out of range")
		return
	}

	plaintext, tokenHash, prefix, err := generateApiToken()
	if err != nil {
		s.logger.Error("api token: generate", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	now := time.Now()
	tok := &domain.ApiToken{
		ID:        uuid.NewString(),
		UserID:    user.ID,
		Name:      req.Name,
		TokenHash: tokenHash,
		Prefix:    prefix,
		Scopes:    normalizeScopes(req.Scopes),
		CreatedAt: now,
	}
	if req.ExpiresInDays > 0 {
		tok.ExpiresAt = now.Add(time.Duration(req.ExpiresInDays) * 24 * time.Hour)
	}
	if err := s.store.CreateApiToken(r.Context(), tok); err != nil {
		s.logger.Error("api token: create", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to create token")
		return
	}

	// AUDIT: token.create (platform-level; the token id is non-secret).
	s.audit(r.Context(), "", "token.create", "api_token", tok.ID, tok.Name)

	writeJSON(w, http.StatusCreated, createTokenView{tokenView: toTokenView(tok), Token: plaintext})
}

// handleListTokens lists the authenticated user's personal access tokens. The
// secret is NEVER returned — only the stored display prefix.
func (s *Server) handleListTokens(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	toks, err := s.store.ListApiTokensByUser(r.Context(), user.ID)
	if err != nil {
		s.logger.Error("api token: list", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to list tokens")
		return
	}
	out := make([]tokenView, 0, len(toks))
	for i := range toks {
		out = append(out, toTokenView(&toks[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

// handleDeleteToken revokes one of the authenticated user's tokens by id. It is
// scoped to the owner, so a user cannot delete another user's token.
func (s *Server) handleDeleteToken(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id := chi.URLParam(r, "id")
	if err := s.store.DeleteApiToken(r.Context(), user.ID, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "token not found")
			return
		}
		s.logger.Error("api token: delete", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to delete token")
		return
	}
	s.audit(r.Context(), "", "token.delete", "api_token", id, "")
	w.WriteHeader(http.StatusNoContent)
}

// normalizeScopes trims, drops empties, and de-duplicates the requested scope
// list, returning a non-nil slice so the stored/returned value is a JSON array
// (never null). Scope ENFORCEMENT is currently coarse: a PAT authenticates as its
// owner and is subject to the same org/project authz as the user; the stored
// scopes are advisory metadata for a future fine-grained check.
func normalizeScopes(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]struct{}{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
