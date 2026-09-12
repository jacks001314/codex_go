package gitutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func runWorktreeGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func initWorktreeRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	runWorktreeGit(t, root, "init")
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("hello\n"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runWorktreeGit(t, root, "add", "file.txt")
	runWorktreeGit(t, root, "commit", "-m", "initial")
	return root
}

func TestLinkedWorktreeCWDsIncludesCorrespondingDirectories(t *testing.T) {
	primary := initWorktreeRepo(t)
	if err := os.MkdirAll(filepath.Join(primary, "pkg"), 0o755); err != nil {
		t.Fatalf("mkdir pkg: %v", err)
	}
	linked := filepath.Join(t.TempDir(), "linked")
	runWorktreeGit(t, primary, "worktree", "add", linked, "-b", "linked-branch")
	if err := os.MkdirAll(filepath.Join(linked, "pkg"), 0o755); err != nil {
		t.Fatalf("mkdir linked pkg: %v", err)
	}

	// Running from a subdirectory preserves the relative directory across the
	// linked checkouts.
	cwds, ok := LinkedWorktreeCWDs(filepath.Join(primary, "pkg"))
	if !ok {
		t.Fatal("LinkedWorktreeCWDs() not ok for a linked checkout")
	}
	seen := map[string]bool{}
	for _, cwd := range cwds {
		resolved, err := filepath.EvalSymlinks(cwd)
		if err != nil {
			t.Fatalf("EvalSymlinks(%q): %v", cwd, err)
		}
		seen[filepath.Clean(resolved)] = true
	}
	linkedPkg, err := filepath.EvalSymlinks(filepath.Join(linked, "pkg"))
	if err != nil {
		t.Fatalf("EvalSymlinks(linked pkg): %v", err)
	}
	if !seen[filepath.Clean(filepath.Join(primary, "pkg"))] || !seen[filepath.Clean(linkedPkg)] {
		t.Fatalf("linked worktree cwds = %#v, want primary and linked pkg dirs", cwds)
	}

	identity, ok := RepositoryIdentityForCWD(primary)
	if !ok {
		t.Fatal("RepositoryIdentityForCWD() not ok for the primary checkout")
	}
	if identity.RelativeCWD != "" {
		t.Fatalf("primary RelativeCWD = %q, want empty", identity.RelativeCWD)
	}
	linkedIdentity, ok := RepositoryIdentityForCWD(linked)
	if !ok {
		t.Fatal("RepositoryIdentityForCWD() not ok for the linked checkout")
	}
	if linkedIdentity.CommonDir != identity.CommonDir {
		t.Fatalf("common dir mismatch: %q vs %q", linkedIdentity.CommonDir, identity.CommonDir)
	}
	if linkedIdentity.PrimaryRoot != identity.PrimaryRoot {
		t.Fatalf("primary root mismatch: %q vs %q", linkedIdentity.PrimaryRoot, identity.PrimaryRoot)
	}
}

func TestRepositoryIdentityRejectsUnrelatedAndUncheckedCheckouts(t *testing.T) {
	primary := initWorktreeRepo(t)
	if _, ok := RepositoryIdentityForCWD(t.TempDir()); ok {
		t.Fatal("a directory outside any repository must have no identity")
	}

	// A `.git` file whose gitdir is not registered under the repository's
	// `worktrees` directory is an unchecked administrative link.
	fake := t.TempDir()
	unregistered := filepath.Join(primary, ".git", "unregistered-gitdir")
	if err := os.MkdirAll(unregistered, 0o755); err != nil {
		t.Fatalf("mkdir unregistered gitdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(fake, ".git"), []byte("gitdir: "+unregistered+"\n"), 0o600); err != nil {
		t.Fatalf("write fake .git: %v", err)
	}
	if _, ok := RepositoryIdentityForCWD(fake); ok {
		t.Fatal("an unregistered gitdir link must not produce an identity")
	}
}
