package manifest

import (
	"os"
	"path/filepath"
	"strings"
)

// Detection is the result of inspecting a directory to guess how it should be
// deployed. It is advisory only: `vortex launch` confirms/overrides it
// interactively and never invents business values.
type Detection struct {
	// HasDockerfile is true when a Dockerfile is present (image/build path).
	HasDockerfile bool
	// Dockerfile is the relative path of the detected Dockerfile, if any.
	Dockerfile string
	// GitRemote is the origin remote URL parsed from .git/config, if present.
	GitRemote string
	// GitBranch is the current branch from .git/HEAD, if resolvable.
	GitBranch string
	// SuggestedName is a DNS-safe app name derived from the directory name.
	SuggestedName string
	// Language is a best-effort runtime guess from marker files (informational).
	Language string
}

// Detect inspects dir for a Dockerfile, a git remote, and language markers.
func Detect(dir string) Detection {
	d := Detection{SuggestedName: SanitizeName(filepath.Base(absOrSelf(dir)))}
	if df := findDockerfile(dir); df != "" {
		d.HasDockerfile = true
		d.Dockerfile = df
	}
	d.GitRemote, d.GitBranch = gitInfo(dir)
	d.Language = detectLanguage(dir)
	return d
}

func absOrSelf(dir string) string {
	if abs, err := filepath.Abs(dir); err == nil {
		return abs
	}
	return dir
}

func findDockerfile(dir string) string {
	for _, name := range []string{"Dockerfile", "dockerfile"} {
		p := filepath.Join(dir, name)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return name
		}
	}
	return ""
}

// languageMarker maps a marker file to a human language label.
type languageMarker struct {
	file string
	lang string
}

func detectLanguage(dir string) string {
	markers := []languageMarker{
		{"go.mod", "Go"},
		{"package.json", "Node.js"},
		{"requirements.txt", "Python"},
		{"pyproject.toml", "Python"},
		{"Gemfile", "Ruby"},
		{"pom.xml", "Java"},
		{"Cargo.toml", "Rust"},
		{"composer.json", "PHP"},
	}
	for _, m := range markers {
		if _, err := os.Stat(filepath.Join(dir, m.file)); err == nil {
			return m.lang
		}
	}
	return ""
}

// gitInfo reads the origin remote URL and current branch from a .git directory
// without shelling out to git. Both values are best-effort and may be empty.
func gitInfo(dir string) (remote, branch string) {
	gitDir := filepath.Join(dir, ".git")
	if fi, err := os.Stat(gitDir); err != nil || !fi.IsDir() {
		return "", ""
	}
	remote = originURL(filepath.Join(gitDir, "config"))
	branch = headBranch(filepath.Join(gitDir, "HEAD"))
	return remote, branch
}

// originURL parses the [remote "origin"] url from a git config file.
func originURL(configPath string) string {
	data, err := os.ReadFile(configPath) // #nosec G304 -- local .git/config path derived from the project dir
	if err != nil {
		return ""
	}
	inOrigin := false
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "[") {
			inOrigin = strings.HasPrefix(line, `[remote "origin"]`)
			continue
		}
		if inOrigin && strings.HasPrefix(line, "url") {
			if _, val, ok := strings.Cut(line, "="); ok {
				return strings.TrimSpace(val)
			}
		}
	}
	return ""
}

// headBranch extracts the branch name from a .git/HEAD ref pointer.
func headBranch(headPath string) string {
	data, err := os.ReadFile(headPath) // #nosec G304 -- local .git/HEAD path derived from the project dir
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(data))
	const refsHeads = "ref: refs/heads/"
	if strings.HasPrefix(line, refsHeads) {
		return strings.TrimPrefix(line, refsHeads)
	}
	return ""
}

// SanitizeName turns an arbitrary string (e.g. a directory name) into a
// lowercase DNS-label-safe app name: [a-z0-9-], no leading/trailing/double
// dashes. An empty result falls back to "app".
func SanitizeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		case r == '-' || r == '_' || r == ' ' || r == '.':
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "app"
	}
	return out
}
