package appserver

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Mirrors Rust #43621: classify primary checkouts (false), validated linked
// worktrees (true), and unknown repositories (null).
func TestThreadIsWorktreeClassifiesLinkedWorktreesLikeRust(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	primary := filepath.Join(root, "primary")
	if err := os.MkdirAll(primary, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit := func(dir string, args ...string) {
		t.Helper()
		command := exec.Command("git", args...)
		command.Dir = dir
		command.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
			"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull,
		)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v error = %v\n%s", args, err, output)
		}
	}
	runGit(primary, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(primary, "file.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(primary, "add", ".")
	runGit(primary, "commit", "-m", "init")
	linked := filepath.Join(root, "linked")
	runGit(primary, "worktree", "add", "--detach", linked)

	if got := threadIsWorktree(context.Background(), primary); got == nil || *got {
		t.Fatalf("primary checkout is_worktree = %v, want false", got)
	}
	if got := threadIsWorktree(context.Background(), linked); got == nil || !*got {
		t.Fatalf("linked worktree is_worktree = %v, want true", got)
	}
	if got := threadIsWorktree(context.Background(), t.TempDir()); got != nil {
		t.Fatalf("unknown repository is_worktree = %v, want null", got)
	}
	if got := threadIsWorktree(context.Background(), ""); got != nil {
		t.Fatalf("empty cwd is_worktree = %v, want null", got)
	}
}
