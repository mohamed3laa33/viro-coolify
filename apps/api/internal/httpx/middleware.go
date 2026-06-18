package httpx

import (
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// normalizeOrigin canonicalizes an origin for comparison: an origin has no path,
// and its scheme+host are case-insensitive, so we lowercase and strip any
// trailing slash. This means an allowlist entry like
// "https://App.Vortex.v60ai.com/" still matches the browser's
// "https://app.vortex.v60ai.com" — a common operator typo that would otherwise
// silently break CORS.
func normalizeOrigin(o string) string {
	return strings.ToLower(strings.TrimRight(strings.TrimSpace(o), "/"))
}

// normalizeOrigins normalizes every entry in an allowlist (preserving "*").
func normalizeOrigins(allowed []string) []string {
	out := make([]string, len(allowed))
	for i, a := range allowed {
		if a == "*" {
			out[i] = "*"
			continue
		}
		out[i] = normalizeOrigin(a)
	}
	return out
}

// corsAllowMethods lists every method the API actually serves (the router uses
// GET/POST/PATCH/PUT/DELETE), plus OPTIONS for preflight.
const corsAllowMethods = "GET, POST, PATCH, PUT, DELETE, OPTIONS"

// corsBaseAllowHeaders are the request headers the browser client is always
// permitted to send. Any additional headers the browser requests via
// Access-Control-Request-Headers are echoed back on top of these.
const corsBaseAllowHeaders = "Content-Type, Authorization, X-Request-Id"

// corsMaxAge is how long (seconds) a browser may cache a preflight result.
const corsMaxAge = "600"

// corsMiddleware applies scoped, credential-safe CORS for the configured origins.
//
// Because the web app authenticates with HttpOnly cookies and runs on a
// different subdomain than the API in production, every browser call is a
// cross-origin credentialed request. For those to work the response MUST:
//   - echo the SPECIFIC request Origin in Access-Control-Allow-Origin (never "*"
//     together with credentials — browsers reject that combination), and only
//     when that Origin is in the allowlist; a non-allowlisted Origin gets no ACAO;
//   - send Access-Control-Allow-Credentials: true;
//   - send Vary: Origin so a shared cache never serves one origin's ACAO to
//     another origin.
//
// A literal "*" in the allowlist is treated as "allow any origin" but, because it
// is incompatible with credentials, it still reflects the specific Origin (not
// "*") and omits Allow-Credentials — useful for token-only/public deployments
// without silently breaking cookie auth.
func corsMiddleware(allowed []string) func(http.Handler) http.Handler {
	wildcard := slices.Contains(allowed, "*")
	norm := normalizeOrigins(allowed)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			// The response always varies by Origin (the ACAO we emit depends on
			// it), so set Vary unconditionally — even for disallowed/absent
			// origins — to keep caches from cross-serving headers. Add (not Set)
			// preserves any Vary a downstream handler may add.
			w.Header().Add("Vary", "Origin")

			allow := origin != "" && (wildcard || slices.Contains(norm, normalizeOrigin(origin)))
			if allow {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				if !wildcard {
					// Credentials are only safe with a specific, allowlisted
					// origin — never with the wildcard mode.
					w.Header().Set("Access-Control-Allow-Credentials", "true")
				}
			}

			if r.Method == http.MethodOptions {
				// Preflight: advertise the supported methods/headers. Only emit
				// these for an allowed origin; a disallowed origin still gets a
				// 204 but no CORS grant, so the browser blocks the real request.
				if allow {
					w.Header().Set("Access-Control-Allow-Methods", corsAllowMethods)
					w.Header().Set("Access-Control-Max-Age", corsMaxAge)
					allowHeaders := corsBaseAllowHeaders
					if req := r.Header.Get("Access-Control-Request-Headers"); req != "" {
						// Echo whatever custom headers the client intends to send,
						// in addition to the always-allowed base set.
						allowHeaders = corsBaseAllowHeaders + ", " + req
					}
					w.Header().Set("Access-Control-Allow-Headers", allowHeaders)
				}
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
	norm := normalizeOrigins(allowed)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if wildcard || !isStateChanging(r.Method) {
				next.ServeHTTP(w, r)
				return
			}
			origin := requestOrigin(r)
			if origin != "" && !slices.Contains(norm, normalizeOrigin(origin)) {
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

// metricsMiddleware records RED metrics for every request into the registry:
// http_requests_total{method,route,status}, the per-route duration histogram, and
// the in-flight gauge. The route label is the chi route PATTERN (e.g.
// "/v1/orgs/{orgID}/apps/{appID}"), NOT the concrete path, so tenant ids never
// land in a label and cardinality stays bounded.
func metricsMiddleware(reg *registry) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			reg.inFlight.inc()
			start := time.Now()
			defer func() {
				reg.inFlight.dec()
				status := ww.Status()
				if status == 0 {
					status = http.StatusOK
				}
				reg.observeRequest(r.Method, routePattern(r), status, time.Since(start).Seconds())
			}()
			next.ServeHTTP(ww, r)
		})
	}
}

// routePattern returns the matched chi route pattern for a request (resolved
// after routing). An unmatched request (404) has no pattern; the registry maps
// that to "unmatched" so a flood of unknown paths cannot explode label cardinality.
func routePattern(r *http.Request) string {
	if rc := chi.RouteContext(r.Context()); rc != nil {
		if p := rc.RoutePattern(); p != "" {
			return p
		}
	}
	return ""
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
