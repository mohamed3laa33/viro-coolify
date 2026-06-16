// Package version holds build metadata, overridable at link time via -ldflags.
package version

var (
	// Version is the semantic version of the build.
	Version = "0.1.0-dev"
	// Commit is the git commit the binary was built from.
	Commit = "unknown"
	// BuildTime is the RFC3339 timestamp the binary was built at.
	BuildTime = "unknown"
)
