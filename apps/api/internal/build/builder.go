// Package build is Vortex's git-source image build pipeline. It turns a tenant's
// git repository into a container image (pushed to the platform registry) which
// the deploy backend (kube.Backend) then rolls out exactly like an image-based
// app. The real implementation (kaniko.go) runs a Kubernetes Job; tests and
// no-cluster fallbacks use FakeBuilder (fake.go), mirroring kube.FakeBackend.
//
// There is NO demo / no-op success path in the real builder: KanikoBuilder
// submits a real Job to a real cluster and fails loudly (with build logs) when
// the build does not succeed.
package build

import "context"

// Request describes a single image build for a git-sourced app.
type Request struct {
	AppID       string // owning app id (correlation / labels)
	BuildID     string // unique build id (correlation; makes the Job name + image tag unique per build)
	OrgSlug     string // sanitized org slug (labels)
	ProjectSlug string // sanitized project slug (labels)
	AppName     string // app name (labels)
	GitRepo     string // https/git clone URL of the source repository
	GitRef      string // branch/ref to build (e.g. "main")
	ContextDir  string // sub-directory within the repo holding the Dockerfile (optional)
	Dockerfile  string // Dockerfile path relative to the context (default "Dockerfile")
	ImageRef    string // fully-qualified push destination, e.g. ghcr.io/acme/app:abc123
	// BuildArgs are passed to kaniko as --build-arg. NOTE: build-args are baked
	// into image layers and visible in the Job spec, so the platform never
	// forwards runtime env/secrets here. Reserved for an explicit, non-secret
	// build-args field if/when one is added.
	BuildArgs map[string]string
}

// Result is the outcome of a successful build.
type Result struct {
	Image string // the pushed image ref (equals Request.ImageRef on success)
}

// Builder turns a git source into a pushed container image. Implementations:
// KanikoBuilder (real, Kubernetes Job) and FakeBuilder (test double).
type Builder interface {
	// Build runs the image build for r to completion, returning the pushed image
	// on success or an error (including captured build logs) on failure. It is
	// expected to be called on a detached, timeout-bound context.
	Build(ctx context.Context, r Request) (Result, error)
}
