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

import (
	"context"
	"strings"
)

// Strategy selects how a git source is turned into a container image. It is the
// single switch that makes this package the ONE coherent build entrypoint: a
// source WITH a Dockerfile builds via Kaniko; a source WITHOUT one builds via
// Cloud Native Buildpacks (Heroku/Fly-class auto-detection). The platform
// resolves it from a shallow probe of the source and sets it on the Request; the
// builder never guesses.
type Strategy string

const (
	// StrategyDockerfile builds from a tenant-supplied Dockerfile (Kaniko path).
	StrategyDockerfile Strategy = "dockerfile"
	// StrategyBuildpacks builds without a Dockerfile via Cloud Native Buildpacks
	// auto-detection.
	StrategyBuildpacks Strategy = "buildpacks"
)

// Request describes a single image build for a git-sourced app.
type Request struct {
	AppID       string // owning app id (correlation / labels)
	BuildID     string // unique build id (correlation; makes the Job name + image tag unique per build)
	OrgSlug     string // sanitized org slug (labels)
	ProjectSlug string // sanitized project slug (labels)
	AppName     string // app name (labels)
	GitRepo     string // https/git clone URL of the source repository
	GitRef      string // branch/ref to build (e.g. "main")
	ContextDir  string // sub-directory within the repo holding the source/Dockerfile (optional)
	Dockerfile  string // Dockerfile path relative to the context (default "Dockerfile"); ignored by the buildpacks strategy
	ImageRef    string // fully-qualified push destination, e.g. ghcr.io/acme/app:abc123

	// Strategy selects the executor. When empty, DetectStrategy(HasDockerfile)
	// chooses one: a present Dockerfile => StrategyDockerfile, else
	// StrategyBuildpacks. The platform sets it after probing the source so the
	// builder never has to guess.
	Strategy Strategy

	// HasDockerfile records whether a Dockerfile was found in the source at the
	// (optional) ContextDir. It is the single detection input the platform
	// resolves (via a shallow git probe) before dispatch; it seeds Strategy when
	// Strategy is left empty.
	HasDockerfile bool

	// Builder optionally overrides the Cloud Native Buildpacks builder image used
	// by the buildpacks strategy (e.g. "paketobuildpacks/builder-jammy-base").
	// Empty falls back to the executor's configured default. Ignored by the
	// Dockerfile strategy.
	Builder string

	// BuildArgs are passed to kaniko as --build-arg (Dockerfile strategy) or
	// surfaced as build-time env to the lifecycle (buildpacks strategy). NOTE:
	// build-args are baked into image layers and visible in the Job spec, so the
	// platform never forwards runtime env/secrets here. Reserved for an explicit,
	// non-secret build-args field if/when one is added.
	BuildArgs map[string]string
}

// Result is the outcome of a successful build.
type Result struct {
	Image    string   // the pushed image ref (equals Request.ImageRef on success)
	Strategy Strategy // the strategy that produced the image (resolved if it was empty)
}

// Builder turns a git source into a pushed container image. Implementations:
// KanikoBuilder (real, Kubernetes Job — Kaniko for Dockerfiles, Cloud Native
// Buildpacks otherwise) and FakeBuilder (test double).
type Builder interface {
	// Build runs the image build for r to completion, returning the pushed image
	// on success or an error (including captured build logs) on failure. It is
	// expected to be called on a detached, timeout-bound context.
	Build(ctx context.Context, r Request) (Result, error)
}

// DetectStrategy picks the build strategy from whether the source has a
// Dockerfile: present => Kaniko (StrategyDockerfile), absent => Cloud Native
// Buildpacks (StrategyBuildpacks). This is the single wiring point for
// no-Dockerfile builds; callers resolve hasDockerfile from the source layout.
func DetectStrategy(hasDockerfile bool) Strategy {
	if hasDockerfile {
		return StrategyDockerfile
	}
	return StrategyBuildpacks
}

// resolveStrategy returns the request's explicit Strategy when set, otherwise
// detects one from HasDockerfile. It is the single dispatch point used by the
// real and fake builders so an unset Strategy still routes correctly.
func (r Request) resolveStrategy() Strategy {
	if s := Strategy(strings.ToLower(strings.TrimSpace(string(r.Strategy)))); s != "" {
		return s
	}
	return DetectStrategy(r.HasDockerfile)
}
