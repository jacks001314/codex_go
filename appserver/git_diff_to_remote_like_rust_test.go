package appserver

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Mirrors Rust #63386 `remote_diff_preserves_slashes_in_the_default_branch_name`:
// a remote whose default branch is `release/stable` must be diffed against that
// branch, not a bogus `stable` ref built from the last path segment.
func TestGitDiffToRemotePreservesSlashesInDefaultBranchLikeRust(t *testing.T) {
	repo := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		command := exec.Command("git", args...)
		command.Dir = repo
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v: %s", args, err, output)
		}
	}
	write := func(name, contents string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, name), []byte(contents), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	runGit("init", "-b", "feature")
	runGit("config", "user.name", "Codex Tests")
	runGit("config", "user.email", "codex-tests@example.com")
	runGit("config", "commit.gpgsign", "false")
	runGit("remote", "add", "origin", ".")
	runGit("commit", "--allow-empty", "-m", "initial")
	runGit("update-ref", "refs/remotes/origin/stable", "HEAD")
	write("release.txt", "released\n")
	runGit("add", "release.txt")
	runGit("commit", "-m", "release")
	runGit("update-ref", "refs/remotes/origin/release/stable", "HEAD")
	runGit("remote", "set-head", "origin", "release/stable")
	release := strings.TrimSpace(mustGitOutput(t, repo, "rev-parse", "HEAD"))
	write("feature.txt", "feature\n")
	runGit("add", "feature.txt")
	runGit("commit", "-m", "unpushed")

	response, err := NewMiscService().GitDiffToRemote(&GitDiffToRemoteParams{CWD: repo})
	if err != nil {
		t.Fatalf("GitDiffToRemote() error = %v", err)
	}
	if response.SHA != release {
		t.Fatalf("SHA = %q, want release commit %q", response.SHA, release)
	}
	if !strings.Contains(response.Diff, "feature.txt") {
		t.Fatalf("diff missing the unpushed feature change:\n%s", response.Diff)
	}
	if strings.Contains(response.Diff, "release.txt") {
		t.Fatalf("diff includes changes already on the default branch:\n%s", response.Diff)
	}
}

func mustGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git %v failed: %v", args, err)
	}
	return string(output)
}
