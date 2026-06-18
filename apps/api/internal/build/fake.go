package build

import (
	"context"
	"sync"
)

// FakeBuilder is an in-memory Builder test double for unit tests in OTHER
// packages (e.g. platform) and the no-cluster fallback in httpx. It records
// every Build request and, by default, "succeeds" by echoing the requested
// ImageRef back as the built image — mirroring kube.FakeBackend.
//
// It is safe for concurrent use.
type FakeBuilder struct {
	mu sync.Mutex

	// Requests records every Build call in order.
	Requests []Request

	// ImageOverride, when non-empty, is returned as Result.Image instead of the
	// request's ImageRef (so tests can assert the platform persists the builder's
	// image, not the pre-computed ref).
	ImageOverride string

	// Err, when non-nil, is returned by Build (simulating a build failure). The
	// request is still recorded.
	Err error
}

var _ Builder = (*FakeBuilder)(nil)

// NewFakeBuilder returns an initialized FakeBuilder that succeeds by default.
func NewFakeBuilder() *FakeBuilder { return &FakeBuilder{} }

// Build records r and returns the configured result/error.
func (f *FakeBuilder) Build(_ context.Context, r Request) (Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Requests = append(f.Requests, r)
	if f.Err != nil {
		return Result{}, f.Err
	}
	img := r.ImageRef
	if f.ImageOverride != "" {
		img = f.ImageOverride
	}
	return Result{Image: img}, nil
}

// Calls returns a copy of the recorded requests (safe for concurrent readers).
func (f *FakeBuilder) Calls() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Request, len(f.Requests))
	copy(out, f.Requests)
	return out
}
