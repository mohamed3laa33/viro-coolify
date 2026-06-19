package kube

import (
	"context"
	"sync"
)

// FakeBuilder is an in-memory Builder test double for unit tests in OTHER
// packages and the no-cluster fallback. It records every Build request and, by
// default, "succeeds" by echoing the requested ImageRef back as the built image
// — mirroring kube.FakeBackend. It still RESOLVES the build strategy
// (Dockerfile => Kaniko, else => buildpacks) so callers can assert the
// no-Dockerfile path was selected.
//
// It is safe for concurrent use.
type FakeBuilder struct {
	mu sync.Mutex

	// Requests records every Build call in order.
	Requests []BuildRequest

	// ImageOverride, when non-empty, is returned as BuildResult.Image instead of
	// the request's ImageRef (so tests can assert the caller persists the
	// builder's image, not the pre-computed ref).
	ImageOverride string

	// Err, when non-nil, is returned by Build (simulating a build failure). The
	// request is still recorded.
	Err error
}

var _ Builder = (*FakeBuilder)(nil)

// NewFakeBuilder returns an initialized FakeBuilder that succeeds by default.
func NewFakeBuilder() *FakeBuilder { return &FakeBuilder{} }

// Build records r and returns the configured result/error. The returned
// Strategy reflects the resolved strategy (explicit or detected), so a test can
// assert a no-Dockerfile request routed to buildpacks without a real cluster.
func (f *FakeBuilder) Build(_ context.Context, r BuildRequest) (BuildResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Requests = append(f.Requests, r)
	if f.Err != nil {
		return BuildResult{}, f.Err
	}
	img := r.ImageRef
	if f.ImageOverride != "" {
		img = f.ImageOverride
	}
	return BuildResult{Image: img, Strategy: r.resolveStrategy()}, nil
}

// Calls returns a copy of the recorded requests (safe for concurrent readers).
func (f *FakeBuilder) Calls() []BuildRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]BuildRequest, len(f.Requests))
	copy(out, f.Requests)
	return out
}
