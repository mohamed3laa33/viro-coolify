package kube

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
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
	return &execHelmRunner{bin: bin}
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
