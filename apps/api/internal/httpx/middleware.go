package httpx

import (
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

// corsMiddleware applies permissive-but-scoped CORS for the configured origins.
func corsMiddleware(allowed []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			wildcard := slices.Contains(allowed, "*")
			if origin != "" && (wildcard || slices.Contains(allowed, origin)) {
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
				if wildcard {
					// A wildcard cannot be combined with credentials per the CORS spec,
					// and reflecting an arbitrary origin with credentials is unsafe.
					w.Header().Set("Access-Control-Allow-Origin", "*")
				} else {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Set("Vary", "Origin")
					w.Header().Set("Access-Control-Allow-Credentials", "true")
				}
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// csrfOriginGuard is a CSRF defense-in-depth layer for cookie-authenticated
// browser requests. For state-changing methods (POST/PUT/PATCH/DELETE) it checks
// the request's Origin (falling back to the Referer's origin) against the
// configured allowlist and rejects mismatches with 403.
//
// Requests with no Origin and no Referer are allowed: native clients (the CLI)
// and server-to-server callers don't send these headers, and they authenticate
// with a Bearer token rather than an ambient cookie, so they aren't subject to
// CSRF. A wildcard "*" in the allowlist disables the check.
func csrfOriginGuard(allowed []string) func(http.Handler) http.Handler {
	wildcard := slices.Contains(allowed, "*")
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if wildcard || !isStateChanging(r.Method) {
				next.ServeHTTP(w, r)
				return
			}
			origin := requestOrigin(r)
			if origin != "" && !slices.Contains(allowed, origin) {
				writeError(w, http.StatusForbidden, "cross-origin request blocked")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// isStateChanging reports whether the HTTP method mutates server state.
func isStateChanging(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// requestOrigin returns the request's origin (scheme://host[:port]) from the
// Origin header, falling back to the origin of the Referer. It returns "" when
// neither is present or parseable (same-origin / native clients).
func requestOrigin(r *http.Request) string {
	if o := r.Header.Get("Origin"); o != "" {
		return o
	}
	ref := r.Header.Get("Referer")
	if ref == "" {
		return ""
	}
	u, err := url.Parse(ref)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// requestLogger logs one structured line per request.
func requestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			reqID := middleware.GetReqID(r.Context())
			if reqID != "" {
				ww.Header().Set("X-Request-Id", reqID)
			}
			start := time.Now()
			defer func() {
				logger.Info("http_request",
					"request_id", reqID,
					"method", r.Method,
					"path", r.URL.Path,
					"status", ww.Status(),
					"bytes", ww.BytesWritten(),
					"duration_ms", time.Since(start).Milliseconds(),
				)
			}()
			next.ServeHTTP(ww, r)
		})
	}
}
