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

// TestStripeReportUsage_QuantityIsCents asserts the cents-vs-hours unit fix:
// ReportUsage posts the quantity it is handed VERBATIM (the same whole-cents the
// caller computes — never re-scaled into "hours"), with action=increment, against
// the per-ITEM usage_records path. This pins the end-to-end unit so a cents value
// produced by Service.ReportUsage lands on Stripe unchanged.
func TestStripeReportUsage_QuantityIsCents(t *testing.T) {
	var gotPath, gotQty, gotAction string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = r.ParseForm()
		gotQty = r.PostFormValue("quantity")
		gotAction = r.PostFormValue("action")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	s := newTestStripe(srv.URL)
	// Caller passes 1234 CENTS of metered usage; the provider must forward it as-is.
	if err := s.ReportUsage(context.Background(), "si_metered_1", 1234, time.Unix(1700000000, 0)); err != nil {
		t.Fatalf("ReportUsage: %v", err)
	}
	if want := "/v1/subscription_items/si_metered_1/usage_records"; gotPath != want {
		t.Fatalf("path = %q, want %q", gotPath, want)
	}
	if gotQty != "1234" {
		t.Fatalf("posted quantity = %q, want %q (whole cents, not hours)", gotQty, "1234")
	}
	if gotAction != "increment" {
		t.Fatalf("action = %q, want increment", gotAction)
	}
}

// TestStripeReportUsage_NoopOnZero asserts a zero/negative quantity makes no HTTP
// call at all (nothing to bill), so an empty metering tick never hits Stripe.
func TestStripeReportUsage_NoopOnZero(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := newTestStripe(srv.URL)
	if err := s.ReportUsage(context.Background(), "si_metered_1", 0, time.Now()); err != nil {
		t.Fatalf("ReportUsage(0): %v", err)
	}
	if err := s.ReportUsage(context.Background(), "si_metered_1", -5, time.Now()); err != nil {
		t.Fatalf("ReportUsage(-5): %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Fatalf("zero/negative quantity must not call Stripe, got %d calls", got)
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
