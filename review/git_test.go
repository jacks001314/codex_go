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

func TestGitDiffProviderMergeBaseWithHeadReturnsSharedCommit(t *testing.T) {
	dir := initGitRepo(t)
	initialBranch := strings.TrimSpace(git(t, dir, "branch", "--show-current"))
	expected := strings.TrimSpace(git(t, dir, "rev-parse", "HEAD"))
	git(t, dir, "checkout", "-b", "feature/review")
	writeFile(t, dir, "feature.txt", "feature change\n")
	git(t, dir, "add", "feature.txt")
	git(t, dir, "commit", "-m", "feature change")
	git(t, dir, "checkout", initialBranch)
	writeFile(t, dir, "base.txt", "base change\n")
	git(t, dir, "add", "base.txt")
	git(t, dir, "commit", "-m", "base change")
	git(t, dir, "checkout", "feature/review")

	provider := &GitDiffProvider{Dir: dir}
	mergeBase, err := provider.MergeBaseWithHead(initialBranch)
	if err != nil {
		t.Fatalf("MergeBaseWithHead returned error: %v", err)
	}
	if mergeBase != expected {
		t.Fatalf("merge base = %q, want %q", mergeBase, expected)
	}
}

func TestGitDiffProviderMergeBaseWithHeadPrefersUpstreamWhenRemoteAhead(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "repo")
	remote := filepath.Join(root, "remote.git")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll repo returned error: %v", err)
	}
	if err := os.MkdirAll(remote, 0o755); err != nil {
		t.Fatalf("MkdirAll remote returned error: %v", err)
	}
	git(t, remote, "init", "--bare")
	git(t, dir, "init", "--initial-branch=main")
	git(t, dir, "config", "user.email", "codex@example.test")
	git(t, dir, "config", "user.name", "Codex Test")
	writeFile(t, dir, "base.txt", "base\n")
	git(t, dir, "add", "base.txt")
	git(t, dir, "commit", "-m", "base commit")
	git(t, dir, "remote", "add", "origin", remote)
	git(t, dir, "push", "-u", "origin", "main")

	git(t, dir, "checkout", "-b", "feature")
	writeFile(t, dir, "feature.txt", "feature change\n")
	git(t, dir, "add", "feature.txt")
	git(t, dir, "commit", "-m", "feature commit")

	git(t, dir, "checkout", "--orphan", "rewrite")
	git(t, dir, "rm", "-rf", ".")
	writeFile(t, dir, "new-main.txt", "rewritten main\n")
	git(t, dir, "add", "new-main.txt")
	git(t, dir, "commit", "-m", "rewrite main")
	git(t, dir, "branch", "-M", "rewrite", "main")
	git(t, dir, "branch", "--set-upstream-to=origin/main", "main")
	git(t, dir, "checkout", "feature")
	git(t, dir, "fetch", "origin")

	expected := strings.TrimSpace(git(t, dir, "merge-base", "HEAD", "origin/main"))
	provider := &GitDiffProvider{Dir: dir}
	mergeBase, err := provider.MergeBaseWithHead("main")
	if err != nil {
		t.Fatalf("MergeBaseWithHead returned error: %v", err)
	}
	if mergeBase != expected {
		t.Fatalf("merge base = %q, want upstream %q", mergeBase, expected)
	}
}

func TestGitDiffProviderMergeBaseWithHeadReturnsEmptyWhenBranchMissing(t *testing.T) {
	dir := initGitRepo(t)
	provider := &GitDiffProvider{Dir: dir}
	mergeBase, err := provider.MergeBaseWithHead("missing-branch")
	if err != nil {
		t.Fatalf("MergeBaseWithHead returned error: %v", err)
	}
	if mergeBase != "" {
		t.Fatalf("merge base = %q, want empty", mergeBase)
	}
}

func TestGitInventoryBranchesAndCommits(t *testing.T) {
	dir := initGitRepo(t)
	initialBranch := strings.TrimSpace(git(t, dir, "branch", "--show-current"))
	git(t, dir, "checkout", "-b", "feature/review")
	writeFile(t, dir, "tracked.txt", "old\nnew\n")
	git(t, dir, "add", "tracked.txt")
	git(t, dir, "commit", "-m", "review change")

	current, branches, err := LocalBranches(dir)
	if err != nil {
		t.Fatalf("LocalBranches returned error: %v", err)
	}
	if current != "feature/review" {
		t.Fatalf("current branch = %q", current)
	}
	if !containsStringReviewTest(branches, "feature/review") || !containsStringReviewTest(branches, initialBranch) {
		t.Fatalf("branches = %#v", branches)
	}

	commits, err := RecentCommits(dir, 1)
	if err != nil {
		t.Fatalf("RecentCommits returned error: %v", err)
	}
	if len(commits) != 1 || commits[0].Subject != "review change" || strings.TrimSpace(commits[0].SHA) == "" {
		t.Fatalf("commits = %#v", commits)
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

func containsStringReviewTest(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
