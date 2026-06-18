package kube

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/retryx"
)

// scriptedHelm returns the queued (err) for each call in order; once the script
// is exhausted it returns nil (success).
type scriptedHelm struct {
	errs  []error
	calls int
}

func (s *scriptedHelm) Run(_ context.Context, _ ...string) (string, error) {
	i := s.calls
	s.calls++
	if i < len(s.errs) {
		return "", s.errs[i]
	}
	return "ok", nil
}

func fastHelmPolicy() retryx.Policy {
	return retryx.Policy{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond}
}

func TestHelmRetry_TransientThenSucceed(t *testing.T) {
	inner := &scriptedHelm{errs: []error{
		errors.New("helm [upgrade]: dial tcp 10.0.0.1:443: connect: connection refused"),
		errors.New("helm [upgrade]: Get \"https://api\": net/http: TLS handshake timeout"),
	}}
	r := newRetryingHelmRunner(inner, fastHelmPolicy())
	out, err := r.Run(context.Background(), "upgrade", "--install", "rel")
	if err != nil {
		t.Fatalf("want success after transient retries, got %v", err)
	}
	if out != "ok" {
		t.Fatalf("want ok, got %q", out)
	}
	if inner.calls != 3 {
		t.Fatalf("want 3 calls (2 transient + 1 ok), got %d", inner.calls)
	}
}

func TestHelmRetry_GiveUpAfterN(t *testing.T) {
	transient := errors.New("helm upgrade: connection reset by peer")
	inner := &scriptedHelm{errs: []error{transient, transient, transient, transient}}
	r := newRetryingHelmRunner(inner, fastHelmPolicy())
	_, err := r.Run(context.Background(), "upgrade", "--install", "rel")
	if err == nil {
		t.Fatalf("want error after exhausting retries")
	}
	if inner.calls != 3 {
		t.Fatalf("want exactly 3 attempts, got %d", inner.calls)
	}
}

func TestHelmRetry_NoRetryOnTerminal(t *testing.T) {
	// A chart/template error is NOT transient and must not be retried.
	inner := &scriptedHelm{errs: []error{
		errors.New("helm [upgrade]: template: common-chart/deployment.yaml: nil pointer evaluating"),
	}}
	r := newRetryingHelmRunner(inner, fastHelmPolicy())
	_, err := r.Run(context.Background(), "upgrade", "--install", "rel")
	if err == nil {
		t.Fatalf("want the terminal error surfaced")
	}
	if inner.calls != 1 {
		t.Fatalf("a non-transient helm failure must not retry: want 1 call, got %d", inner.calls)
	}
}

func TestHelmRetryable_Classification(t *testing.T) {
	retryable := []string{
		"connection refused", "i/o timeout", "deadline exceeded",
		"unexpected EOF", "the server was unable to return a response",
		"503 Service Unavailable", "dial tcp: lookup api",
	}
	for _, m := range retryable {
		if !helmRetryable(errors.New(m)) {
			t.Errorf("expected %q to be retryable", m)
		}
	}
	terminal := []string{
		"template: parse error", "values don't meet the specifications",
		"release: already exists", "INSTALLATION FAILED: invalid chart",
	}
	for _, m := range terminal {
		if helmRetryable(errors.New(m)) {
			t.Errorf("expected %q to be terminal", m)
		}
	}
}
