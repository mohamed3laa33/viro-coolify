package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope", "config.yaml")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load missing: %v", err)
	}
	if cfg.APIURL != DefaultAPIURL {
		t.Fatalf("expected default API URL, got %q", cfg.APIURL)
	}
	if cfg.LoggedIn() {
		t.Fatal("fresh config should not be logged in")
	}
}

func TestSaveAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg.APIURL = "https://api.vortex.example"
	cfg.CurrentOrg = "org-1"
	cfg.CurrentProj = "proj-1"
	if err := cfg.SetTokens("acc", "ref"); err != nil {
		t.Fatalf("SetTokens: %v", err)
	}

	// File must exist with restrictive perms (it holds tokens).
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("expected 0600 perms, got %o", perm)
	}

	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.APIURL != "https://api.vortex.example" {
		t.Errorf("APIURL not persisted: %q", reloaded.APIURL)
	}
	if reloaded.CurrentOrg != "org-1" || reloaded.CurrentProj != "proj-1" {
		t.Errorf("context not persisted: %+v", reloaded)
	}
	if reloaded.AccessToken != "acc" || reloaded.RefreshToken != "ref" {
		t.Errorf("tokens not persisted: %+v", reloaded)
	}
	if !reloaded.LoggedIn() {
		t.Error("reloaded config should be logged in")
	}
}

func TestClearPreservesContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg, _ := Load(path)
	cfg.CurrentOrg = "org-1"
	_ = cfg.SetTokens("acc", "ref")

	if err := cfg.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if cfg.LoggedIn() {
		t.Error("Clear should remove tokens")
	}
	reloaded, _ := Load(path)
	if reloaded.LoggedIn() {
		t.Error("cleared tokens should not persist")
	}
	if reloaded.CurrentOrg != "org-1" {
		t.Errorf("Clear should preserve context, got %q", reloaded.CurrentOrg)
	}
}

func TestSetPATPersistsAndClearsJWT(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg, _ := Load(path)
	_ = cfg.SetTokens("acc", "ref")

	if err := cfg.SetPAT("vrt_abc123"); err != nil {
		t.Fatalf("SetPAT: %v", err)
	}
	// SetPAT must clear any JWT session so the two never coexist.
	if cfg.AccessToken != "" || cfg.RefreshToken != "" {
		t.Fatalf("SetPAT should clear JWT tokens: %+v", cfg)
	}
	if !cfg.LoggedIn() {
		t.Fatal("a stored PAT should count as logged in")
	}
	reloaded, _ := Load(path)
	if reloaded.Token != "vrt_abc123" {
		t.Fatalf("PAT not persisted: %q", reloaded.Token)
	}
	// Clear must remove the PAT too.
	if err := reloaded.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if reloaded.LoggedIn() || reloaded.Token != "" {
		t.Fatalf("Clear should remove the PAT: %+v", reloaded)
	}
}

func TestDefaultPathRespectsEnv(t *testing.T) {
	t.Setenv("VORTEX_CONFIG", "/tmp/custom/vortex.yaml")
	p, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	if p != "/tmp/custom/vortex.yaml" {
		t.Fatalf("expected env override, got %q", p)
	}
}
