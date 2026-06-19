package kube

// This file adds the no-Dockerfile (Heroku/Fly-class) image build path to the
// kube package: a git source WITHOUT a Dockerfile is built via Cloud Native
// Buildpacks (or nixpacks) auto-detection, in addition to the existing
// Dockerfile/Kaniko path. The Builder seam mirrors kube.Backend — KubeBuilder
// (real, hardened Kubernetes Job) and FakeBuilder (in-memory test double) — so a
// caller can detect the strategy from the source layout and dispatch to the
// right executor without leaking Kubernetes details.
//
// There is NO demo / no-op success path in the real builder: KubeBuilder submits
// a real Job to a real cluster and fails loudly (with build logs) when the build
// does not succeed. Use FakeBuilder for unit tests in OTHER packages.

import (
	"context"
	"strings"
)

// BuildStrategy selects how a git source is turned into an image.
type BuildStrategy string

const (
	// StrategyDockerfile builds from a tenant-supplied Dockerfile (Kaniko path).
	StrategyDockerfile BuildStrategy = "dockerfile"
	// StrategyBuildpacks builds without a Dockerfile via Cloud Native Buildpacks /
	// nixpacks auto-detection (Heroku/Fly-class convenience).
	StrategyBuildpacks BuildStrategy = "buildpacks"
)

// BuildRequest describes a single image build for a git-sourced app. It mirrors
// the platform's build request shape so a caller can dispatch by strategy.
type BuildRequest struct {
	AppID       string // owning app id (correlation / labels)
	BuildID     string // unique build id (makes the Job name + image tag unique per build)
	OrgSlug     string // sanitized org slug (labels)
	ProjectSlug string // sanitized project slug (labels)
	AppName     string // app name (labels)

	GitRepo string // https/git clone URL of the source repository
	GitRef  string // branch/ref to build (e.g. "main")

	// ContextDir is the sub-directory within the repo to build from (optional).
	// For the Dockerfile strategy it is the Dockerfile context; for buildpacks it
	// is the application source root.
	ContextDir string

	// Strategy selects the executor. When empty, DetectStrategy(HasDockerfile)
	// chooses one: a present Dockerfile => StrategyDockerfile, else
	// StrategyBuildpacks.
	Strategy BuildStrategy

	// HasDockerfile records whether a Dockerfile was found in the source at the
	// (optional) ContextDir. It is the single detection input the caller resolves
	// (e.g. via a shallow git probe) before dispatch; the builder never guesses.
	HasDockerfile bool

	// Dockerfile is the Dockerfile path relative to the context (default
	// "Dockerfile"). Ignored by the buildpacks strategy.
	Dockerfile string

	// Builder optionally overrides the Cloud Native Buildpacks builder image used
	// by the buildpacks strategy (e.g. "paketobuildpacks/builder-jammy-base").
	// Empty falls back to the executor's configured default. Ignored by the
	// Dockerfile strategy.
	Builder string

	// ImageRef is the fully-qualified push destination, e.g.
	// ghcr.io/acme/app:abc123.
	ImageRef string

	// BuildArgs are non-secret build-time arguments. NOTE: build-args are baked
	// into image layers and visible in the Job spec, so the platform never
	// forwards runtime env/secrets here. For the buildpacks strategy these are
	// surfaced as build-time environment to the lifecycle.
	BuildArgs map[string]string
}

// BuildResult is the outcome of a successful build.
type BuildResult struct {
	Image    string        // the pushed image ref (equals BuildRequest.ImageRef on success)
	Strategy BuildStrategy // the strategy that produced the image (resolved if it was empty)
}

// Builder turns a git source into a pushed container image. Implementations:
// KubeBuilder (real, Kubernetes Job — Kaniko for Dockerfiles, buildpacks
// otherwise) and FakeBuilder (test double).
type Builder interface {
	// Build runs the image build for r to completion, returning the pushed image
	// on success or an error (including captured build logs) on failure. It is
	// expected to be called on a detached, timeout-bound context.
	Build(ctx context.Context, r BuildRequest) (BuildResult, error)
}

// DetectStrategy picks the build strategy from whether the source has a
// Dockerfile: present => Kaniko (StrategyDockerfile), absent => Cloud Native
// Buildpacks (StrategyBuildpacks). This is the single wiring point for
// no-Dockerfile builds; callers resolve hasDockerfile from the source layout.
func DetectStrategy(hasDockerfile bool) BuildStrategy {
	if hasDockerfile {
		return StrategyDockerfile
	}
	return StrategyBuildpacks
}

// resolveStrategy returns the request's explicit Strategy when set, otherwise
// detects one from HasDockerfile. It is the dispatch point used by KubeBuilder
// and FakeBuilder so an unset Strategy still routes correctly.
func (r BuildRequest) resolveStrategy() BuildStrategy {
	if s := BuildStrategy(strings.ToLower(strings.TrimSpace(string(r.Strategy)))); s != "" {
		return s
	}
	return DetectStrategy(r.HasDockerfile)
}
