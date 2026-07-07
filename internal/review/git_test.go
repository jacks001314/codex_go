package review

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitDiffProviderUncommittedIncludesTrackedAndUntracked(t *testing.T) {
	dir := initGitRepo(t)
	writeFile(t, dir, "tracked.txt", "old\nnew\n")
	writeFile(t, dir, "untracked.txt", "fresh\n")

	provider := &GitDiffProvider{Dir: dir}
	diff, err := provider.Diff(Target{Kind: "uncommitted"})
	if err != nil {
		t.Fatalf("Diff returned error: %v", err)
	}
	if !strings.Contains(diff, "diff --git a/tracked.txt b/tracked.txt") || !strings.Contains(diff, "+new") {
		t.Fatalf("tracked change missing from diff:\n%s", diff)
	}
	if !strings.Contains(diff, "diff --git a/untracked.txt b/untracked.txt") || !strings.Contains(diff, "+fresh") {
		t.Fatalf("untracked file missing from diff:\n%s", diff)
	}
}

func TestGitDiffProviderCommit(t *testing.T) {
	dir := initGitRepo(t)
	writeFile(t, dir, "tracked.txt", "old\nnew\n")
	git(t, dir, "add", "tracked.txt")
	git(t, dir, "commit", "-m", "change tracked")
	sha := strings.TrimSpace(git(t, dir, "rev-parse", "HEAD"))

	provider := &GitDiffProvider{Dir: dir}
	diff, err := provider.Diff(Target{Kind: "commit", Commit: sha})
	if err != nil {
		t.Fatalf("Diff returned error: %v", err)
	}
	if !strings.Contains(diff, "change tracked") || !strings.Contains(diff, "+new") {
		t.Fatalf("commit diff missing expected content:\n%s", diff)
	}
}

func initGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	git(t, dir, "init")
	git(t, dir, "config", "user.email", "codex@example.test")
	git(t, dir, "config", "user.name", "Codex Test")
	writeFile(t, dir, "tracked.txt", "old\n")
	git(t, dir, "add", "tracked.txt")
	git(t, dir, "commit", "-m", "initial")
	return dir
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(output))
	}
	return string(output)
}
