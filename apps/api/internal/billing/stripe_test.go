package billing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/retryx"
)

// newTestStripe points a StripeProvider at a test server with a fast retry policy
// so retry tests don't sleep meaningfully.
func newTestStripe(serverURL string) *StripeProvider {
	s := NewStripeProvider("sk_test", "https://ok", "https://cancel")
	s.baseURL = serverURL + "/v1"
	s.retry = retryx.Policy{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond}
	return s
}

func TestStripePost_RetryThenSucceed(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			w.WriteHeader(http.StatusBadGateway) // 502: transient
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"cus_123"}`))
	}))
	defer srv.Close()

	s := newTestStripe(srv.URL)
	id, err := s.EnsureCustomer(context.Background(), "org-1", "a@b.com")
	if err != nil {
		t.Fatalf("want success after retries, got %v", err)
	}
	if id != "cus_123" {
		t.Fatalf("want cus_123, got %q", id)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("want 3 attempts (2 transient + 1 ok), got %d", got)
	}
}

func TestStripePost_GiveUpAfterN(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError) // always 500
	}))
	defer srv.Close()

	s := newTestStripe(srv.URL)
	_, err := s.EnsureCustomer(context.Background(), "org-1", "a@b.com")
	if err == nil {
		t.Fatalf("want error after exhausting retries")
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("want exactly 3 attempts on persistent 5xx, got %d", got)
	}
}

func TestStripePost_NoRetryOn4xx(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest) // 400: terminal client error
		_, _ = w.Write([]byte(`{"error":{"message":"bad"}}`))
	}))
	defer srv.Close()

	s := newTestStripe(srv.URL)
	_, err := s.EnsureCustomer(context.Background(), "org-1", "a@b.com")
	if err == nil {
		t.Fatalf("want error on 400")
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("a 4xx must not be retried: want 1 attempt, got %d", got)
	}
}

func TestStripePost_RetryOn429(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&calls, 1) < 2 {
			w.WriteHeader(http.StatusTooManyRequests) // 429: transient (rate limit)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"cus_ok"}`))
	}))
	defer srv.Close()

	s := newTestStripe(srv.URL)
	if _, err := s.EnsureCustomer(context.Background(), "org-1", "a@b.com"); err != nil {
		t.Fatalf("429 should be retried then succeed, got %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("want 2 attempts (1x 429 + 1 ok), got %d", got)
	}
}

func TestStripePost_ContextCancelled(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	s := newTestStripe(srv.URL)
	s.retry = retryx.Policy{MaxAttempts: 5, BaseDelay: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled
	_, err := s.EnsureCustomer(ctx, "org-1", "a@b.com")
	if err == nil {
		t.Fatalf("want error when ctx is cancelled")
	}
	// At most one round-trip; the backoff wait then aborts on the cancelled ctx.
	if got := atomic.LoadInt32(&calls); got > 1 {
		t.Fatalf("cancelled ctx must not keep retrying, got %d attempts", got)
	}
}
