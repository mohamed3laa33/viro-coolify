// Package config loads and persists the vortex CLI configuration stored at
// ~/.vortex/config.yaml. It holds the API base URL, the access/refresh tokens
// returned by the control-plane auth endpoints, and the persisted org/project
// context used by commands when --org/--project are not given.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// DefaultAPIURL is the control-plane base URL used when none is configured.
const DefaultAPIURL = "http://localhost:8080"

// Config is the on-disk CLI configuration.
type Config struct {
	APIURL       string `yaml:"api_url"`
	AccessToken  string `yaml:"access_token,omitempty"`
	RefreshToken string `yaml:"refresh_token,omitempty"`
	CurrentOrg   string `yaml:"current_org,omitempty"`
	CurrentProj  string `yaml:"current_project,omitempty"`

	// path is the file the config was loaded from / will be saved to. It is not
	// serialized.
	path string `yaml:"-"`
}

// DefaultPath returns the default config path: $VORTEX_CONFIG when set, else
// ~/.vortex/config.yaml.
func DefaultPath() (string, error) {
	if p := os.Getenv("VORTEX_CONFIG"); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".vortex", "config.yaml"), nil
}

// Load reads the config at path. A missing file yields a zero-value config
// (with defaults applied) rather than an error, so first-run works.
func Load(path string) (*Config, error) {
	cfg := &Config{path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			cfg.applyDefaults()
			return cfg, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	cfg.path = path
	cfg.applyDefaults()
	return cfg, nil
}

// LoadDefault loads the config from DefaultPath.
func LoadDefault() (*Config, error) {
	path, err := DefaultPath()
	if err != nil {
		return nil, err
	}
	return Load(path)
}

func (c *Config) applyDefaults() {
	if c.APIURL == "" {
		c.APIURL = DefaultAPIURL
	}
}

// Path returns the file this config is bound to.
func (c *Config) Path() string { return c.path }

// Save writes the config back to its path, creating the parent directory and
// using 0600 permissions because the file stores bearer tokens.
func (c *Config) Save() error {
	if c.path == "" {
		return errors.New("config has no path")
	}
	dir := filepath.Dir(c.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	if err := os.WriteFile(c.path, data, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// SetTokens records the access + refresh tokens (e.g. after login/signup) and
// persists the config.
func (c *Config) SetTokens(access, refresh string) error {
	c.AccessToken = access
	c.RefreshToken = refresh
	return c.Save()
}

// Clear removes auth tokens (logout) and persists the config. Context (org/
// project) and the API URL are preserved.
func (c *Config) Clear() error {
	c.AccessToken = ""
	c.RefreshToken = ""
	return c.Save()
}

// LoggedIn reports whether an access token is present.
func (c *Config) LoggedIn() bool { return c.AccessToken != "" }
