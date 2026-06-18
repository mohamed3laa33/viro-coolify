package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// okHandler is a trivial downstream handler used to detect whether the CORS
// middleware passed the request through (200) or short-circuited it.
func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func varyContainsOrigin(h http.Header) bool {
	for _, v := range h.Values("Vary") {
		for _, f := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(f), "Origin") {
				return true
			}
		}
	}
	return false
}

// TestCORSPreflightAllowedOrigin asserts a preflight from an allowlisted origin
// returns 204 with the SPECIFIC origin echoed, credentials enabled, Vary:Origin,
// the full method set, the Max-Age and the base + echoed request headers.
func TestCORSPreflightAllowedOrigin(t *testing.T) {
	const origin = "https://app.vortex.v60ai.com"
	mw := corsMiddleware([]string{"http://localhost:3000", origin})

	req := httptest.NewRequest(http.MethodOptions, "/v1/apps", nil)
	req.Header.Set("Origin", origin)
	req.Header.Set("Access-Control-Request-Method", "DELETE")
	req.Header.Set("Access-Control-Request-Headers", "X-Custom-Thing")
	rec := httptest.NewRecorder()

	mw(okHandler()).ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != origin {
		t.Fatalf("ACAO = %q, want %q (must echo the specific origin, never *)", got, origin)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("Allow-Credentials = %q, want true", got)
	}
	if !varyContainsOrigin(rec.Header()) {
		t.Fatalf("Vary missing Origin: %q", rec.Header().Values("Vary"))
	}
	methods := rec.Header().Get("Access-Control-Allow-Methods")
	for _, m := range []string{"GET", "POST", "PATCH", "PUT", "DELETE", "OPTIONS"} {
		if !strings.Contains(methods, m) {
			t.Fatalf("Allow-Methods %q missing %s", methods, m)
		}
	}
	if got := rec.Header().Get("Access-Control-Max-Age"); got == "" {
		t.Fatal("Access-Control-Max-Age not set on preflight")
	}
	allowHeaders := rec.Header().Get("Access-Control-Allow-Headers")
	for _, h := range []string{"Content-Type", "Authorization", "X-Request-Id", "X-Custom-Thing"} {
		if !strings.Contains(allowHeaders, h) {
			t.Fatalf("Allow-Headers %q missing %s", allowHeaders, h)
		}
	}
}

// TestCORSPreflightDisallowedOrigin asserts a preflight from a non-allowlisted
// origin short-circuits 204 with NO CORS grant (no ACAO), so the browser blocks
// the real request.
func TestCORSPreflightDisallowedOrigin(t *testing.T) {
	mw := corsMiddleware([]string{"http://localhost:3000"})

	req := httptest.NewRequest(http.MethodOptions, "/v1/apps", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()

	mw(okHandler()).ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204 (OPTIONS must short-circuit)", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("ACAO = %q, want empty for a disallowed origin", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Fatalf("Allow-Credentials = %q, want empty for a disallowed origin", got)
	}
	// Vary:Origin must still be present so caches don't serve this (no-ACAO)
	// response to an allowed origin.
	if !varyContainsOrigin(rec.Header()) {
		t.Fatalf("Vary missing Origin on disallowed preflight: %q", rec.Header().Values("Vary"))
	}
}

// TestCORSActualRequestAllowedOrigin asserts a real (non-OPTIONS) request from an
// allowed origin is passed through to the handler AND carries the credentialed
// CORS headers on the response.
func TestCORSActualRequestAllowedOrigin(t *testing.T) {
	const origin = "http://localhost:3000"
	mw := corsMiddleware([]string{origin})

	req := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	req.Header.Set("Origin", origin)
	rec := httptest.NewRecorder()

	passed := false
	mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		passed = true
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)

	if !passed {
		t.Fatal("actual request was not passed to the handler")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != origin {
		t.Fatalf("ACAO = %q, want %q", got, origin)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("Allow-Credentials = %q, want true", got)
	}
	if !varyContainsOrigin(rec.Header()) {
		t.Fatalf("Vary missing Origin: %q", rec.Header().Values("Vary"))
	}
}

// TestCORSActualRequestDisallowedOrigin asserts a real request from a non-allowed
// origin still reaches the handler (CORS is enforced by the browser, not the
// server, for non-preflighted requests) but carries NO ACAO so the browser hides
// the response from the page.
func TestCORSActualRequestDisallowedOrigin(t *testing.T) {
	mw := corsMiddleware([]string{"http://localhost:3000"})

	req := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	rec := httptest.NewRecorder()

	mw(okHandler()).ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("ACAO = %q, want empty for a disallowed origin", got)
	}
}

// TestCORSWildcardNeverWithCredentials asserts that even in wildcard ("*") mode
// the middleware reflects the specific origin and omits credentials, never
// emitting the spec-forbidden "*" + Allow-Credentials combination.
func TestCORSWildcardNeverWithCredentials(t *testing.T) {
	const origin = "https://anything.example.com"
	mw := corsMiddleware([]string{"*"})

	req := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	req.Header.Set("Origin", origin)
	rec := httptest.NewRecorder()

	mw(okHandler()).ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != origin {
		t.Fatalf("ACAO = %q, want the reflected origin %q (not \"*\")", got, origin)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Fatalf("Allow-Credentials = %q, want empty in wildcard mode", got)
	}
}

// TestCORSNoOriginHeader asserts a same-origin / native-client request (no Origin
// header) is passed through untouched with no ACAO.
func TestCORSNoOriginHeader(t *testing.T) {
	mw := corsMiddleware([]string{"http://localhost:3000"})

	req := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	rec := httptest.NewRecorder()

	passed := false
	mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		passed = true
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)

	if !passed {
		t.Fatal("no-Origin request was not passed to the handler")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("ACAO = %q, want empty when no Origin header is present", got)
	}
}
