// Package retryx is a tiny, dependency-free retry-with-backoff helper for the
// control plane's flaky OUTBOUND calls (the helm runner, the Stripe HTTP
// provider). It deliberately uses only the standard library.
//
// The caller classifies each failure as retryable or terminal: a transient
// failure (network error, 5xx) is worth retrying with exponential backoff; a
// terminal failure (a 4xx, a non-idempotent error, a validation error) is
// returned immediately without burning attempts. The retry loop is bounded
// (MaxAttempts) and always honors context cancellation/deadline between attempts
// — it never sleeps past a cancelled context.
package retryx

import (
	"context"
	"errors"
	// math/rand here is used only for retry timing jitter (anti-thundering-herd),
	// not as a security primitive; crypto/rand would be cargo-cult and slower.
	// nosemgrep: go.lang.security.audit.crypto.math_random.math-random-used
	"math/rand"
	"time"
)

// Policy bounds a retry loop. The zero value is NOT usable; use DefaultPolicy or
// set MaxAttempts>0 explicitly.
type Policy struct {
	// MaxAttempts is the total number of attempts (NOT retries): MaxAttempts=3
	// means one initial try plus up to two retries. Values <1 are treated as 1.
	MaxAttempts int
	// BaseDelay is the first backoff sleep; it doubles each retry up to MaxDelay.
	BaseDelay time.Duration
	// MaxDelay caps the per-retry backoff. 0 means no cap.
	MaxDelay time.Duration
	// Jitter, when true, randomizes each backoff in [BaseDelay/2, BaseDelay] of the
	// computed step to avoid a thundering herd. It uses math/rand (non-crypto: this
	// is timing jitter, not a security primitive).
	Jitter bool
}

// DefaultPolicy is a conservative bounded policy: 3 attempts, 200ms base, capped
// at 2s, with jitter. Suitable for an outbound HTTP/exec call behind an idempotent
// operation.
func DefaultPolicy() Policy {
	return Policy{MaxAttempts: 3, BaseDelay: 200 * time.Millisecond, MaxDelay: 2 * time.Second, Jitter: true}
}

// terminal wraps an error so Do stops retrying immediately and unwraps to the
// original cause. Callers signal a non-retryable failure with Terminal(err).
type terminal struct{ err error }

func (t terminal) Error() string { return t.err.Error() }
func (t terminal) Unwrap() error { return t.err }

// Terminal marks err as non-retryable: Do returns it immediately (after
// unwrapping) without consuming further attempts. A nil err yields nil.
func Terminal(err error) error {
	if err == nil {
		return nil
	}
	return terminal{err: err}
}

// IsTerminal reports whether err was marked Terminal.
func IsTerminal(err error) bool {
	var t terminal
	return errors.As(err, &t)
}

// Do runs fn up to policy.MaxAttempts times. fn should return:
//   - nil on success (Do returns nil immediately),
//   - Terminal(err) for a non-retryable failure (Do returns the unwrapped err
//     immediately, no more attempts),
//   - any other error to be retried with exponential backoff until attempts are
//     exhausted (Do then returns that last error).
//
// Between attempts Do waits the backoff OR until ctx is done, whichever first; a
// cancelled/expired ctx aborts with ctx.Err() and stops retrying. fn is always
// called at least once (even with a already-cancelled ctx) so a fast success is
// not blocked.
func Do(ctx context.Context, policy Policy, fn func(ctx context.Context) error) error {
	attempts := policy.MaxAttempts
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		err := fn(ctx)
		if err == nil {
			return nil
		}
		// A terminal failure short-circuits: unwrap and return without retrying.
		var t terminal
		if errors.As(err, &t) {
			return t.err
		}
		lastErr = err
		// No sleep after the final attempt.
		if attempt == attempts-1 {
			break
		}
		if werr := wait(ctx, backoff(policy, attempt)); werr != nil {
			return werr
		}
	}
	return lastErr
}

// backoff computes the delay before the retry following the given (0-based)
// attempt index: BaseDelay * 2^attempt, capped at MaxDelay, optionally jittered.
func backoff(p Policy, attempt int) time.Duration {
	base := p.BaseDelay
	if base <= 0 {
		base = 100 * time.Millisecond
	}
	d := base
	for i := 0; i < attempt; i++ {
		d *= 2
		if p.MaxDelay > 0 && d >= p.MaxDelay {
			d = p.MaxDelay
			break
		}
	}
	if p.MaxDelay > 0 && d > p.MaxDelay {
		d = p.MaxDelay
	}
	if p.Jitter && d > 0 {
		// Full-ish jitter in [d/2, d] to spread retries without collapsing backoff.
		half := d / 2
		d = half + time.Duration(rand.Int63n(int64(d-half)+1)) //nolint:gosec // G404: timing jitter, not security
	}
	return d
}

// wait sleeps for d or until ctx is done, returning ctx.Err() if ctx fires first.
func wait(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
