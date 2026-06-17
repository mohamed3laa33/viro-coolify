// Package version holds the CLI's build version, injectable at build time via
// -ldflags "-X .../internal/version.Version=v1.2.3".
package version

// Version is the CLI version. It defaults to "dev" and is overridden at build
// time via ldflags.
var Version = "dev"

// Commit is the git commit the binary was built from (set via ldflags).
var Commit = "none"
