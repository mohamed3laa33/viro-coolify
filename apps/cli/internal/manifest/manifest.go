// Package manifest reads and writes the per-directory Vortex app manifest
// (vortex.yaml) used by `vortex launch` / `vortex deploy`. The manifest captures
// only the deployment *intent* the user chose locally (app name, project/org
// context, container image or git source). It deliberately holds no business
// values — CPU/memory/quota defaults come from the control-plane API + admin
// panel (invariant #1); a 0/empty resource field means "let the server decide".
package manifest

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Filename is the canonical manifest file name written into a project directory.
const Filename = "vortex.yaml"

// Manifest is the on-disk vortex.yaml. Empty optional fields are omitted so a
// scaffolded file stays minimal and readable.
type Manifest struct {
	// App is the application name (DNS-safe; becomes <app>.<project>.<org>...).
	App string `yaml:"app"`
	// Org / Project pin the deployment context by name or id. Empty means "use
	// the CLI's current context" at deploy time.
	Org     string `yaml:"org,omitempty"`
	Project string `yaml:"project,omitempty"`

	// Build is the source of the workload: either a prebuilt container image or a
	// git repository the control plane builds from. Exactly one is expected.
	Build Build `yaml:"build"`

	// CPU / MemoryMB are optional overrides. Zero means the server applies the
	// admin-configured plan default (never hardcoded here — invariant #1).
	CPU      float64 `yaml:"cpu,omitempty"`
	MemoryMB int     `yaml:"memoryMb,omitempty"`

	// path is the file the manifest was loaded from / will be written to.
	path string `yaml:"-"`
}

// Build describes where the workload comes from.
type Build struct {
	// Image is a prebuilt container image reference (e.g. "nginx:1.27").
	Image string `yaml:"image,omitempty"`
	// GitRepository + GitBranch build from source via the control plane.
	GitRepository string `yaml:"gitRepository,omitempty"`
	GitBranch     string `yaml:"gitBranch,omitempty"`
	// Buildpack optionally selects a builder for git-source apps.
	Buildpack string `yaml:"buildpack,omitempty"`
}

// ErrNotFound is returned by Load when no manifest exists at the given path.
var ErrNotFound = errors.New("no vortex.yaml manifest found")

// PathIn returns the manifest path inside dir.
func PathIn(dir string) string { return filepath.Join(dir, Filename) }

// Load reads the manifest at path. A missing file yields ErrNotFound so callers
// can distinguish "not initialised" from a real parse/IO error.
func Load(path string) (*Manifest, error) {
	// The manifest path is the user's project file (flag or vortex.yaml in the
	// working dir); reading it is the entire purpose of this loader.
	data, err := os.ReadFile(path) // #nosec G304 -- user-controlled project manifest path by design
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("read manifest %s: %w", path, err)
	}
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest %s: %w", path, err)
	}
	m.path = path
	return &m, nil
}

// LoadIn loads the manifest from dir.
func LoadIn(dir string) (*Manifest, error) { return Load(PathIn(dir)) }

// Path returns the file this manifest is bound to (empty until Save/Load set it).
func (m *Manifest) Path() string { return m.path }

// Validate checks the manifest is internally consistent before a deploy.
func (m *Manifest) Validate() error {
	if m.App == "" {
		return errors.New("manifest: app name is required")
	}
	if m.Build.Image == "" && m.Build.GitRepository == "" {
		return errors.New("manifest: build.image or build.gitRepository is required")
	}
	return nil
}

// Save writes the manifest to path (0644 — it carries no secrets) and records it.
func (m *Manifest) Save(path string) error {
	data, err := yaml.Marshal(m)
	if err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	header := "# Vortex app manifest — see `vortex launch`.\n" +
		"# Resource defaults (cpu/memory/quota) come from your plan via the API;\n" +
		"# leave cpu/memoryMb unset to use the admin-configured defaults.\n"
	// 0644 is intentional: the manifest carries no secrets and is meant to be
	// committed to the repo and readable in CI, like a Dockerfile or package.json.
	if err := os.WriteFile(path, append([]byte(header), data...), 0o644); err != nil { // #nosec G306 -- non-secret, committable project manifest
		return fmt.Errorf("write manifest %s: %w", path, err)
	}
	m.path = path
	return nil
}
