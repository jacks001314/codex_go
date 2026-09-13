package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Rust's default_worktree_base: a fresh repository resolves its checked-out
// branch, a remote HEAD wins (origin first), and unrelated non-UTF-8 refs are
// ignored when decoding the chosen base.
func TestDefaultWorktreeBaseLikeRust(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	source := t.TempDir()
	runGit(t, source, "init", "-b", "main")
	runGit(t, source, "config", "user.email", "test@example.com")
	runGit(t, source, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(source, "file.txt"), []byte("hello\n"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runGit(t, source, "add", "file.txt")
	runGit(t, source, "commit", "-m", "initial")

	base, err := DefaultWorktreeBase(source)
	if err != nil {
		t.Fatalf("DefaultWorktreeBase() error = %v", err)
	}
	if base != "refs/heads/main" {
		t.Fatalf("DefaultWorktreeBase() = %q, want refs/heads/main", base)
	}

	// A remote HEAD wins over the conventional refs, even when the remote ref
	// name is not main or master.
	runGit(t, source, "update-ref", "refs/remotes/origin/release/stable", "HEAD")
	runGit(t, source, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/release/stable")
	base, err = DefaultWorktreeBase(source)
	if err != nil {
		t.Fatalf("DefaultWorktreeBase() with origin HEAD error = %v", err)
	}
	if base != "refs/remotes/origin/release/stable" {
		t.Fatalf("DefaultWorktreeBase() = %q, want refs/remotes/origin/release/stable", base)
	}

	// An unrelated packed ref with non-UTF-8 bytes must not break resolution.
	head, err := gitStdout(source, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("rev-parse error = %v", err)
	}
	packedRefs := filepath.Join(source, ".git", "packed-refs")
	if err := os.WriteFile(packedRefs, []byte(head+" refs/heads/unrelated-\xff\n"), 0o600); err != nil {
		t.Fatalf("write packed-refs: %v", err)
	}
	base, err = DefaultWorktreeBase(source)
	if err != nil {
		t.Fatalf("DefaultWorktreeBase() with non-UTF-8 refs error = %v", err)
	}
	if base != "refs/remotes/origin/release/stable" {
		t.Fatalf("DefaultWorktreeBase() after packed refs = %q", base)
	}
}

// A repository with no conventional branch reports Rust's error.
func TestDefaultWorktreeBaseWithoutConventionalBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	source := t.TempDir()
	runGit(t, source, "init", "-b", "trunk")
	runGit(t, source, "config", "user.email", "test@example.com")
	runGit(t, source, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(source, "file.txt"), []byte("hello\n"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runGit(t, source, "add", "file.txt")
	runGit(t, source, "commit", "-m", "initial")

	if _, err := DefaultWorktreeBase(source); err == nil {
		t.Fatal("DefaultWorktreeBase() succeeded without a default branch")
	}
}

// Rust's worktree session check compares the requested and applied checkout
// directories, resolving symlinks when both exist.
func TestPathsEqualResolvesSymlinksLikeRust(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "checkout")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if !PathsEqual(nested, filepath.Join(root, "checkout")) {
		t.Fatal("identical paths must compare equal")
	}
	if !PathsEqual(nested, filepath.Join(root, ".", "checkout", "..", "checkout")) {
		t.Fatal("unclean but equivalent paths must compare equal")
	}
	if PathsEqual(nested, filepath.Join(root, "other")) {
		t.Fatal("different paths compared equal")
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(nested, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if !PathsEqual(link, nested) {
		t.Fatal("a symlink to a checkout must compare equal to the checkout")
	}
}
