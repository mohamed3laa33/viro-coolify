package kube

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/retryx"
)

// HelmRunner runs `helm` subcommands. The default implementation execs the real
// `helm` binary; tests inject a mock that captures args (and the values file).
type HelmRunner interface {
	Run(ctx context.Context, args ...string) (stdout string, err error)
}

// execHelmRunner shells out to the `helm` binary on PATH.
type execHelmRunner struct {
	// bin is the helm executable name/path; defaults to "helm".
	bin string
}

// NewExecHelmRunner returns a HelmRunner backed by the `helm` binary. Pass an
// empty bin to use "helm" from PATH.
func NewExecHelmRunner(bin string) HelmRunner {
	if bin == "" {
		bin = "helm"
	}
	// Wrap the real binary in a bounded retry-with-backoff. `helm upgrade
	// --install` (the only mutating command the backend issues) is IDEMPOTENT, so a
	// TRANSIENT failure (a connection reset to the API server, a timeout, an
	// i/o-timeout pulling the chart) is safe to retry. Non-transient helm failures
	// (a chart/template/validation error) are classified terminal and NOT retried.
	return newRetryingHelmRunner(&execHelmRunner{bin: bin}, retryx.DefaultPolicy())
}

// retryingHelmRunner decorates a HelmRunner with a bounded retry-with-backoff for
// TRANSIENT failures. It is applied around the real `helm` binary only (tests
// inject their own un-wrapped mock runners), so a transient connection error to
// the cluster does not fail an otherwise-recoverable idempotent deploy.
type retryingHelmRunner struct {
	inner  HelmRunner
	policy retryx.Policy
}

func newRetryingHelmRunner(inner HelmRunner, policy retryx.Policy) HelmRunner {
	return &retryingHelmRunner{inner: inner, policy: policy}
}

func (r *retryingHelmRunner) Run(ctx context.Context, args ...string) (string, error) {
	var out string
	err := retryx.Do(ctx, r.policy, func(ctx context.Context) error {
		var rerr error
		out, rerr = r.inner.Run(ctx, args...)
		if rerr == nil {
			return nil
		}
		if helmRetryable(rerr) {
			return rerr // transient: let retryx back off and try again
		}
		return retryx.Terminal(rerr) // template/validation/etc: do not retry
	})
	return out, err
}

// helmTransientMarkers are substrings of a helm/kubectl error message that signal
// a TRANSIENT, retry-safe failure (a network/connection/timeout class error) for
// the idempotent `helm upgrade --install`. A helm error NOT matching any of these
// (e.g. a chart template error, a values validation error, an immutable-field
// conflict) is treated as terminal and not retried.
var helmTransientMarkers = []string{
	"connection refused",
	"connection reset",
	"no route to host",
	"timeout",
	"timed out",
	"i/o timeout",
	"deadline exceeded",
	"eof",
	"tls handshake",
	"temporary failure",
	"server is currently unable",
	"the server was unable to return a response",
	"too many requests",
	"503",
	"502",
	"504",
	"unexpected eof",
	"connection closed",
	"dial tcp",
}

// helmRetryable reports whether a helm Run error looks transient (retry-safe).
func helmRetryable(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, m := range helmTransientMarkers {
		if strings.Contains(msg, m) {
			return true
		}
	}
	return false
}

func (r *execHelmRunner) Run(ctx context.Context, args ...string) (string, error) {
	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command -- args built internally, no shell
	cmd := exec.CommandContext(ctx, r.bin, args...) //nolint:gosec // G204: no shell; args are constructed internally
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("helm %v: %w: %s", args, err, stderr.String())
	}
	return stdout.String(), nil
}

// observedHelmRunner decorates a HelmRunner, invoking an onRun callback after each
// execution with whether it failed. It lets an external observer (the control
// plane's metrics registry) count helm execs/failures WITHOUT the kube package
// depending on the metrics layer.
type observedHelmRunner struct {
	inner HelmRunner
	onRun func(failed bool)
}

// NewObservedHelmRunner wraps inner so onRun(failed) is called after every Run.
// A nil onRun or nil inner is tolerated (the wrapper is then a pass-through / uses
// the real helm binary), so callers never need to nil-check.
func NewObservedHelmRunner(inner HelmRunner, onRun func(failed bool)) HelmRunner {
	if inner == nil {
		inner = NewExecHelmRunner("")
	}
	return &observedHelmRunner{inner: inner, onRun: onRun}
}

func (r *observedHelmRunner) Run(ctx context.Context, args ...string) (string, error) {
	out, err := r.inner.Run(ctx, args...)
	if r.onRun != nil {
		r.onRun(err != nil)
	}
	return out, err
}
