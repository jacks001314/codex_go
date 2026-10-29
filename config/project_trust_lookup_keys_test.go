package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestNormalizedProjectTrustKeysLikeRust pins Rust's normalized_project_trust_keys
// (codex-rs/config/src/loader/mod.rs): the canonical spelling is tried before the
// original one, both use the host path convention, and a path whose spellings
// agree yields a single key.
func TestNormalizedProjectTrustKeysLikeRust(t *testing.T) {
	dir := t.TempDir()
	clean := filepath.Join(dir, "repo")
	sep := string(os.PathSeparator)
	aliased := dir + sep + "repo" + sep + ".." + sep + "repo"
	keys := NormalizedProjectTrustKeys(aliased)
	if len(keys) != 2 {
		t.Fatalf("NormalizedProjectTrustKeys(%q) = %#v, want canonical + original", aliased, keys)
	}
	wantCanonical := CanonicalProjectTrustPath(aliased)
	if keys[0] != wantCanonical {
		t.Fatalf("first key = %q, want canonical %q", keys[0], wantCanonical)
	}
	wantOriginal := aliased
	if runtime.GOOS == "windows" {
		wantOriginal = strings.ToLower(aliased)
	}
	if keys[1] != wantOriginal {
		t.Fatalf("second key = %q, want original spelling %q", keys[1], wantOriginal)
	}
	if got := NormalizedProjectTrustKeys(clean); len(got) != 1 || got[0] != CanonicalProjectTrustPath(clean) {
		t.Fatalf("NormalizedProjectTrustKeys(%q) = %#v, want one canonical key", clean, got)
	}
	if got := NormalizedProjectTrustKeys("  "); len(got) != 1 || got[0] != "" {
		t.Fatalf("NormalizedProjectTrustKeys(blank) = %#v", got)
	}
	if runtime.GOOS == "windows" {
		if canonical := CanonicalProjectTrustPath(strings.ToUpper(dir)); canonical != strings.ToLower(canonical) {
			t.Fatalf("Windows canonical key %q must be ASCII-lowercased", canonical)
		}
	}
}

// TestFindProjectRootLikeRust pins Rust's find_project_root
// (codex-rs/config/src/loader/mod.rs): the nearest marker ancestor, or cwd when
// none exists. An empty marker list disables discovery, so a repository still
// falls back to cwd.
func TestFindProjectRootLikeRust(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	nested := filepath.Join(repo, "nested")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		t.Fatalf("write HEAD: %v", err)
	}
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	if got := FindProjectRoot(nested, DefaultProjectRootMarkers()); got != repo {
		t.Fatalf("FindProjectRoot(nested) = %q, want %q", got, repo)
	}
	if got := FindProjectRoot(nested, nil); got != nested {
		t.Fatalf("FindProjectRoot(nested, no markers) = %q, want cwd %q", got, nested)
	}

	// A custom marker replaces the VCS defaults.
	custom := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(filepath.Join(custom, ".company-root"), 0o755); err != nil {
		t.Fatalf("mkdir marker: %v", err)
	}
	child := filepath.Join(custom, "child")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatalf("mkdir child: %v", err)
	}
	if got := FindProjectRoot(child, []string{".company-root"}); got != custom {
		t.Fatalf("FindProjectRoot(custom markers) = %q, want %q", got, custom)
	}
}

// TestGitRootForTrustLikeRust pins Rust's resolve_root_git_project_for_trust
// (codex-rs/git-utils/src/trust.rs): the nearest Git checkout root, or absence
// outside a repository.
func TestGitRootForTrustLikeRust(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	nested := filepath.Join(repo, "src")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		t.Fatalf("write HEAD: %v", err)
	}
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	root, ok := GitRootForTrust(nested)
	if !ok || root != repo {
		t.Fatalf("GitRootForTrust(nested) = %q, %v; want %q", root, ok, repo)
	}
	// A bare .git directory without HEAD is not repository metadata
	// (Rust #39629), so it must not become a trust root.
	fake := filepath.Join(t.TempDir(), "fake")
	if err := os.MkdirAll(filepath.Join(fake, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir fake .git: %v", err)
	}
	if root, ok := GitRootForTrust(fake); ok {
		t.Fatalf("GitRootForTrust(fake) = %q, want no root", root)
	}
}

// TestActiveProjectKeyReturnsSavedSpellingLikeRust covers the key-returning
// lookup the TUI trust target uses: the projects table spelling is preserved.
func TestActiveProjectKeyReturnsSavedSpellingLikeRust(t *testing.T) {
	dir := t.TempDir()
	spelled := filepath.Join(dir, "Repo")
	projects := map[string]any{spelled: map[string]any{"trust_level": "untrusted"}}
	key, project, ok := ProjectTrustLookupFromNativePath(spelled).ActiveProjectKey(projects)
	if !ok || key != spelled || project["trust_level"] != "untrusted" {
		t.Fatalf("ActiveProjectKey() = %q, %#v, %v", key, project, ok)
	}
	if key, _, ok := ProjectTrustLookupFromNativePath(spelled).ActiveProjectKey(nil); ok || key != "" {
		t.Fatalf("ActiveProjectKey(nil) = %q, %v", key, ok)
	}
}
