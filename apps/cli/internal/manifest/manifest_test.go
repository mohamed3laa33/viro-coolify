package manifest

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	m := &Manifest{
		App:     "web",
		Org:     "acme",
		Project: "default",
		Build:   Build{Image: "nginx:1.27"},
	}
	path := PathIn(dir)
	if err := m.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := LoadIn(dir)
	if err != nil {
		t.Fatalf("LoadIn: %v", err)
	}
	if got.App != "web" || got.Org != "acme" || got.Build.Image != "nginx:1.27" {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	if got.Path() != path {
		t.Fatalf("Path() = %q, want %q", got.Path(), path)
	}
}

func TestLoadMissingReturnsErrNotFound(t *testing.T) {
	_, err := LoadIn(t.TempDir())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		m       Manifest
		wantErr bool
	}{
		{"ok image", Manifest{App: "a", Build: Build{Image: "x"}}, false},
		{"ok git", Manifest{App: "a", Build: Build{GitRepository: "https://x"}}, false},
		{"no name", Manifest{Build: Build{Image: "x"}}, true},
		{"no source", Manifest{App: "a"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.m.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate() err=%v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestManifestHasNoHardcodedResources(t *testing.T) {
	// A scaffolded manifest must not carry business values (invariant #1): with
	// no overrides, cpu/memory stay zero so the server applies plan defaults.
	dir := t.TempDir()
	m := &Manifest{App: "web", Build: Build{Image: "nginx"}}
	if err := m.Save(PathIn(dir)); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := LoadIn(dir)
	if err != nil {
		t.Fatalf("LoadIn: %v", err)
	}
	if got.CPU != 0 || got.MemoryMB != 0 {
		t.Fatalf("expected zero resources, got cpu=%v mem=%v", got.CPU, got.MemoryMB)
	}
}

func TestSanitizeName(t *testing.T) {
	cases := map[string]string{
		"My App":        "my-app",
		"my_app":        "my-app",
		"  Web Server ": "web-server",
		"a..b":          "a-b",
		"---x---":       "x",
		"":              "app",
		"!!!":           "app",
		"Foo.Bar_Baz":   "foo-bar-baz",
	}
	for in, want := range cases {
		if got := SanitizeName(in); got != want {
			t.Errorf("SanitizeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDetectDockerfileAndLanguage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	det := Detect(dir)
	if !det.HasDockerfile {
		t.Fatal("expected Dockerfile detection")
	}
	if det.Language != "Go" {
		t.Fatalf("expected Go language, got %q", det.Language)
	}
	if det.SuggestedName == "" {
		t.Fatal("expected a suggested name")
	}
}

func TestDetectGitRemoteAndBranch(t *testing.T) {
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	cfg := "[core]\n\trepositoryformatversion = 0\n" +
		"[remote \"origin\"]\n\turl = https://github.com/me/app.git\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n"
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write git config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/develop\n"), 0o644); err != nil {
		t.Fatalf("write HEAD: %v", err)
	}
	det := Detect(dir)
	if det.GitRemote != "https://github.com/me/app.git" {
		t.Fatalf("unexpected remote: %q", det.GitRemote)
	}
	if det.GitBranch != "develop" {
		t.Fatalf("unexpected branch: %q", det.GitBranch)
	}
}

func TestDetectNoMarkers(t *testing.T) {
	det := Detect(t.TempDir())
	if det.HasDockerfile || det.GitRemote != "" || det.Language != "" {
		t.Fatalf("expected empty detection, got %+v", det)
	}
}
