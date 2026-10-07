package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDiscoverProjectRootPreservesAbsenceLikeRust covers Rust #49160's promoted
// `discover_project_root` (codex-rs/config/src/loader/mod.rs:1505): the marker
// walk reports absence instead of falling back to cwd, while
// `find_project_root` (activeProjectRootWithMarkers) keeps the cwd fallback.
func TestDiscoverProjectRootPreservesAbsenceLikeRust(t *testing.T) {
	root := t.TempDir()
	plain := filepath.Join(root, "plain", "sub")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatalf("mkdir plain: %v", err)
	}

	if got, found := DiscoverProjectRoot(plain, []string{".git"}); found || got != "" {
		t.Fatalf("DiscoverProjectRoot(unmarked dir) = %q/%v, want absence", got, found)
	}
	// An empty marker list means "no discovery at all".
	if got, found := DiscoverProjectRoot(plain, nil); found || got != "" {
		t.Fatalf("DiscoverProjectRoot(no markers) = %q/%v, want absence", got, found)
	}
	// The cwd fallback stays find_project_root's job.
	if got := activeProjectRootWithMarkers(plain, []string{".git"}); got != plain {
		t.Fatalf("activeProjectRootWithMarkers(unmarked dir) = %q, want cwd %q", got, plain)
	}

	repo := filepath.Join(root, "repo")
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
	got, found := DiscoverProjectRoot(nested, []string{".git"})
	if !found || got != repo {
		t.Fatalf("DiscoverProjectRoot(git child) = %q/%v, want %q/true", got, found, repo)
	}
	// A synthetic .git directory without HEAD is not repository metadata
	// (Rust #39629), so discovery keeps walking.
	synthetic := filepath.Join(root, "synthetic", "inner")
	if err := os.MkdirAll(filepath.Join(synthetic, "..", ".git"), 0o755); err != nil {
		t.Fatalf("mkdir synthetic .git: %v", err)
	}
	if _, found := DiscoverProjectRoot(synthetic, []string{".git"}); found {
		t.Fatal("DiscoverProjectRoot(synthetic .git) reported a root, want absence")
	}
}

// TestDiscoveredProjectlessFolderLikeRust covers the two-probe projectless
// classification #49160 added to the TUI (codex-rs/tui/src/config_update.rs):
// configured markers and default markers must both find no project root.
func TestDiscoveredProjectlessFolderLikeRust(t *testing.T) {
	root := t.TempDir()
	plain := filepath.Join(root, "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatalf("mkdir plain: %v", err)
	}
	if !DiscoveredProjectlessFolder(plain, nil) {
		t.Fatal("unmarked directory should be projectless")
	}
	if !DiscoveredProjectlessFolder(plain, []string{".codex-project"}) {
		t.Fatal("unmarked directory should stay projectless for custom markers")
	}

	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir repo/.git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		t.Fatalf("write repo HEAD: %v", err)
	}
	if DiscoveredProjectlessFolder(repo, nil) {
		t.Fatal("git checkout should not be projectless")
	}

	// A gitfile checkout stays a project when configured markers exclude Git
	// (upstream `remote_project_trust_guards_thread_start_and_preserves_repository_decision`).
	gitfile := filepath.Join(root, "gitfile")
	if err := os.MkdirAll(gitfile, 0o755); err != nil {
		t.Fatalf("mkdir gitfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(gitfile, ".git"), []byte("gitdir: ../separate-git-dir\n"), 0o600); err != nil {
		t.Fatalf("write gitfile: %v", err)
	}
	if DiscoveredProjectlessFolder(gitfile, nil) {
		t.Fatal("gitfile checkout should not be projectless when default markers include Git")
	}
}
