package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/auth"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/config"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/identity"
)

// Auth cookie names. The frontend matches these exactly. Tokens are also still
// returned in the JSON body so header-based clients (the CLI) keep working.
const (
	accessCookieName  = "vortex_access"
	refreshCookieName = "vortex_refresh"
)

type ctxKey int

const userCtxKey ctxKey = iota

// setAuthCookies writes the access and refresh tokens as HttpOnly cookies using
// the platform cookie contract: SameSite=Lax, Path=/, Secure in production, and
// (in production) a Domain of the configured base domain so the cookies are
// shared across "*.<base>" subdomains. In dev the Domain is omitted so cookies
// are host-only on localhost. Cookie MaxAge mirrors the corresponding token TTL.
func setAuthCookies(w http.ResponseWriter, access, refresh string, cfg *config.Config) {
	http.SetCookie(w, authCookie(accessCookieName, access, cfg.JWTAccessTTL*60, cfg))
	http.SetCookie(w, authCookie(refreshCookieName, refresh, cfg.JWTRefreshTTL*3600, cfg))
}

// clearAuthCookies expires both auth cookies (MaxAge<0 deletes immediately).
func clearAuthCookies(w http.ResponseWriter, cfg *config.Config) {
	http.SetCookie(w, authCookie(accessCookieName, "", -1, cfg))
	http.SetCookie(w, authCookie(refreshCookieName, "", -1, cfg))
}

func authCookie(name, value string, maxAge int, cfg *config.Config) *http.Cookie {
	c := &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   maxAge,
	}
	if cfg.IsProduction() {
		c.Secure = true
		c.Domain = cfg.BaseDomain
	}
	if maxAge < 0 {
		// Belt-and-suspenders: also set an expiry in the past for deletion.
		c.Expires = time.Unix(0, 0)
	}
	return c
}

// accessTokenFromRequest extracts the access token, preferring the HttpOnly
// "vortex_access" cookie (browser clients) and falling back to the
// "Authorization: Bearer" header (the CLI and other API clients) for backward
// compatibility.
func accessTokenFromRequest(r *http.Request) string {
	if c, err := r.Cookie(accessCookieName); err == nil && c.Value != "" {
		return c.Value
	}
	parts := strings.SplitN(r.Header.Get("Authorization"), " ", 2)
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return parts[1]
	}
	return ""
}

// refreshTokenFromRequest returns the refresh token, preferring the cookie and
// falling back to the JSON body value already decoded by the caller.
func refreshTokenFromRequest(r *http.Request, bodyToken string) string {
	if c, err := r.Cookie(refreshCookieName); err == nil && c.Value != "" {
		return c.Value
	}
	return bodyToken
}

func userFromContext(ctx context.Context) (*domain.User, bool) {
	u, ok := ctx.Value(userCtxKey).(*domain.User)
	return u, ok
}

// authMiddleware requires a valid access token and loads the user into the context.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := accessTokenFromRequest(r)
		if token == "" {
			writeError(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		claims, err := s.tokens.Verify(token, auth.AccessToken)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid or expired token")
			return
		}
		user, err := s.identity.GetUser(r.Context(), claims.Subject)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unknown user")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userCtxKey, user)))
	})
}

// adminMiddleware requires the authenticated user to be a Viro super-admin.
// It returns 401 when unauthenticated and 403 when the user is not an admin.
func (s *Server) adminMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := userFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if !user.IsAdmin {
			writeError(w, http.StatusForbidden, "super-admin access required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- request / response shapes ---

type signupRequest struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Password string `json:"password"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type refreshRequest struct {
	RefreshToken string `json:"refreshToken"`
}

type userView struct {
	ID      string `json:"id"`
	Email   string `json:"email"`
	Name    string `json:"name"`
	IsAdmin bool   `json:"isAdmin"`
}

func toUserView(u *domain.User) userView {
	return userView{ID: u.ID, Email: u.Email, Name: u.Name, IsAdmin: u.IsAdmin}
}

type authResponse struct {
	User         userView `json:"user"`
	AccessToken  string   `json:"accessToken"`
	RefreshToken string   `json:"refreshToken"`
}

func toAuthResponse(res *identity.AuthResult) authResponse {
	return authResponse{
		User:         toUserView(res.User),
		AccessToken:  res.Access,
		RefreshToken: res.Refresh,
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	// Reject a body that contains more than a single JSON value (e.g. trailing
	// garbage or a second concatenated object): Decode reads only the first
	// value, so without this check "{} junk" would be silently accepted.
	if dec.More() {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}

// writeAuthError maps identity errors to HTTP status codes.
func writeAuthError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, identity.ErrEmailTaken):
		writeError(w, http.StatusConflict, "email already registered")
	case errors.Is(err, identity.ErrInvalidCredentials):
		writeError(w, http.StatusUnauthorized, "invalid email or password")
	case errors.Is(err, identity.ErrValidation):
		writeError(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), "identity: "))
	case errors.Is(err, identity.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden")
	default:
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

func (s *Server) handleSignup(w http.ResponseWriter, r *http.Request) {
	var req signupRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	res, err := s.identity.Signup(r.Context(), req.Email, req.Name, req.Password)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	setAuthCookies(w, res.Access, res.Refresh, s.cfg)
	writeJSON(w, http.StatusCreated, toAuthResponse(res))
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	res, err := s.identity.Login(r.Context(), req.Email, req.Password)
	if err != nil {
		// AUDIT: a failed login (wrong password / unknown user). Record only the
		// attempted email — never the password. Platform-level (no org).
		if errors.Is(err, identity.ErrInvalidCredentials) {
			s.auditAs(r.Context(), "", normalizeAuditEmail(req.Email), "", "auth.login_failed", "user", "", "")
		}
		writeAuthError(w, err)
		return
	}
	s.auditAs(r.Context(), res.User.ID, res.User.Email, "", "auth.login", "user", res.User.ID, "")
	setAuthCookies(w, res.Access, res.Refresh, s.cfg)
	writeJSON(w, http.StatusOK, toAuthResponse(res))
}

// normalizeAuditEmail lowercases/trims an attempted email for an audit record.
func normalizeAuditEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	// Refresh accepts the token from the "vortex_refresh" cookie (browser) or the
	// JSON body (CLI). The body is optional so cookie-only callers need not send one.
	var req refreshRequest
	if r.ContentLength != 0 && r.Body != http.NoBody {
		if !decodeJSON(w, r, &req) {
			return
		}
	}
	token := refreshTokenFromRequest(r, req.RefreshToken)
	res, err := s.identity.Refresh(r.Context(), token)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	// Rotation issued a fresh pair; set the new cookies (old refresh is revoked).
	setAuthCookies(w, res.Access, res.Refresh, s.cfg)
	writeJSON(w, http.StatusOK, toAuthResponse(res))
}

// handleLogout revokes the caller's current refresh token (read from the
// "vortex_refresh" cookie) and clears both auth cookies. It is authenticated and
// idempotent — a missing/invalid refresh token still clears cookies and 204s.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	token := refreshTokenFromRequest(r, "")
	if err := s.identity.Logout(r.Context(), token); err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if u, ok := userFromContext(r.Context()); ok && u != nil {
		s.auditAs(r.Context(), u.ID, u.Email, "", "auth.logout", "user", u.ID, "")
	}
	clearAuthCookies(w, s.cfg)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	writeJSON(w, http.StatusOK, toUserView(user))
}
