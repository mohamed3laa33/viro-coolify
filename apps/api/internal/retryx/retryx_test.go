package retryx

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fastPolicy keeps backoff tiny so tests don't actually sleep meaningfully.
func fastPolicy(attempts int) Policy {
	return Policy{MaxAttempts: attempts, BaseDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond}
}

func TestDo_RetryThenSucceed(t *testing.T) {
	calls := 0
	err := Do(context.Background(), fastPolicy(3), func(context.Context) error {
		calls++
		if calls < 3 {
			return errors.New("transient")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("want success, got %v", err)
	}
	if calls != 3 {
		t.Fatalf("want 3 calls, got %d", calls)
	}
}

func TestDo_GiveUpAfterN(t *testing.T) {
	calls := 0
	sentinel := errors.New("still failing")
	err := Do(context.Background(), fastPolicy(3), func(context.Context) error {
		calls++
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("want sentinel after giving up, got %v", err)
	}
	if calls != 3 {
		t.Fatalf("want exactly 3 attempts, got %d", calls)
	}
}

func TestDo_NoRetryOnTerminal(t *testing.T) {
	calls := 0
	sentinel := errors.New("4xx terminal")
	err := Do(context.Background(), fastPolicy(5), func(context.Context) error {
		calls++
		return Terminal(sentinel)
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("want unwrapped terminal error, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("terminal must not retry: want 1 call, got %d", calls)
	}
	if IsTerminal(err) {
		t.Fatalf("Do must unwrap the terminal marker before returning")
	}
}

func TestDo_FirstAttemptSuccess(t *testing.T) {
	calls := 0
	if err := Do(context.Background(), fastPolicy(3), func(context.Context) error {
		calls++
		return nil
	}); err != nil {
		t.Fatalf("want nil, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("want single call on immediate success, got %d", calls)
	}
}

func TestDo_ContextCancelStopsRetries(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err := Do(ctx, Policy{MaxAttempts: 5, BaseDelay: time.Hour}, func(context.Context) error {
		calls++
		cancel() // cancel during the first attempt so the backoff wait aborts
		return errors.New("transient")
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("want 1 call before ctx-cancel aborts the backoff, got %d", calls)
	}
}

func TestDo_AttemptsClampedToOne(t *testing.T) {
	calls := 0
	_ = Do(context.Background(), Policy{MaxAttempts: 0}, func(context.Context) error {
		calls++
		return errors.New("x")
	})
	if calls != 1 {
		t.Fatalf("MaxAttempts<1 must still run once, got %d", calls)
	}
}

func TestTerminalNilIsNil(t *testing.T) {
	if Terminal(nil) != nil {
		t.Fatalf("Terminal(nil) must be nil")
	}
}

func TestBackoffExponentialCapped(t *testing.T) {
	p := Policy{BaseDelay: 100 * time.Millisecond, MaxDelay: 400 * time.Millisecond}
	if got := backoff(p, 0); got != 100*time.Millisecond {
		t.Fatalf("attempt0 want 100ms, got %v", got)
	}
	if got := backoff(p, 1); got != 200*time.Millisecond {
		t.Fatalf("attempt1 want 200ms, got %v", got)
	}
	if got := backoff(p, 5); got != 400*time.Millisecond {
		t.Fatalf("attempt5 should be capped at 400ms, got %v", got)
	}
}
