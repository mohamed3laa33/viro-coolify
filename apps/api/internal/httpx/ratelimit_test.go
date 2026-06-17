package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestRateLimitReturns429AfterLimit verifies the middleware allows up to `limit`
// requests per window per client and rejects the next one with 429.
func TestRateLimitReturns429AfterLimit(t *testing.T) {
	const limit = 3
	mw := rateLimit(limit, time.Minute)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	newReq := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil)
		r.RemoteAddr = "203.0.113.5:54321"
		return r
	}

	for i := 0; i < limit; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, newReq())
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: got %d, want 200", i+1, rec.Code)
		}
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newReq())
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("over-limit request: got %d, want 429", rec.Code)
	}
}

// TestRateLimitPerClient verifies different client IPs have independent budgets.
func TestRateLimitPerClient(t *testing.T) {
	mw := rateLimit(1, time.Minute)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := func(ip string) int {
		r := httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil)
		r.RemoteAddr = ip + ":1000"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)
		return rec.Code
	}

	if code := req("198.51.100.1"); code != http.StatusOK {
		t.Fatalf("client A first request: got %d, want 200", code)
	}
	if code := req("198.51.100.2"); code != http.StatusOK {
		t.Fatalf("client B first request: got %d, want 200", code)
	}
	if code := req("198.51.100.1"); code != http.StatusTooManyRequests {
		t.Fatalf("client A second request: got %d, want 429", code)
	}
}

// TestRateLimitWindowResets verifies the window resets after it elapses.
func TestRateLimitWindowResets(t *testing.T) {
	l := newFixedWindowLimiter(1, time.Minute)
	now := time.Now()
	if !l.allow("k", now) {
		t.Fatal("first request should be allowed")
	}
	if l.allow("k", now) {
		t.Fatal("second request in window should be denied")
	}
	if !l.allow("k", now.Add(time.Minute)) {
		t.Fatal("request after window should be allowed")
	}
}
