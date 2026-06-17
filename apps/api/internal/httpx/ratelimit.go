package httpx

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// Auth/webhook rate-limit defaults: a small fixed window per client IP. These
// are named constants (not magic numbers) so the policy is easy to tune.
const (
	// authRateLimit is the max number of requests allowed per window per IP.
	authRateLimit = 10
	// authRateWindow is the length of the fixed window.
	authRateWindow = time.Minute
)

// fixedWindowLimiter is a tiny, dependency-free, in-memory fixed-window rate
// limiter keyed by client. It is safe for concurrent use.
type fixedWindowLimiter struct {
	limit  int
	window time.Duration

	mu      sync.Mutex
	buckets map[string]*windowBucket
}

type windowBucket struct {
	count       int
	windowStart time.Time
}

func newFixedWindowLimiter(limit int, window time.Duration) *fixedWindowLimiter {
	return &fixedWindowLimiter{
		limit:   limit,
		window:  window,
		buckets: make(map[string]*windowBucket),
	}
}

// allow reports whether a request from key is permitted at time now, advancing
// the window and bumping the count when it is. It also opportunistically prunes
// stale buckets to bound memory.
func (l *fixedWindowLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[key]
	if !ok || now.Sub(b.windowStart) >= l.window {
		l.buckets[key] = &windowBucket{count: 1, windowStart: now}
		l.pruneLocked(now)
		return true
	}
	if b.count >= l.limit {
		return false
	}
	b.count++
	return true
}

// pruneLocked drops buckets whose window has fully elapsed. Caller holds l.mu.
func (l *fixedWindowLimiter) pruneLocked(now time.Time) {
	for k, b := range l.buckets {
		if now.Sub(b.windowStart) >= l.window {
			delete(l.buckets, k)
		}
	}
}

// clientKey derives a stable client identifier from the request. RealIP
// middleware (installed in routes) normalizes RemoteAddr from trusted proxy
// headers; we strip the port so the key is the bare IP.
func clientKey(r *http.Request) string {
	addr := r.RemoteAddr
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}

// rateLimit returns a chi-compatible middleware enforcing limit requests per
// window per client IP. Over-limit requests get a 429 JSON error.
func rateLimit(limit int, window time.Duration) func(http.Handler) http.Handler {
	limiter := newFixedWindowLimiter(limit, window)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !limiter.allow(clientKey(r), time.Now()) {
				writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
