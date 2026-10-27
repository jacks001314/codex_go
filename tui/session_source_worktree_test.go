package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSessionPickerWorktreeCWDsGating(t *testing.T) {
	if got := SessionPickerWorktreeCWDs("", true); got != nil {
		t.Fatalf("empty cwd = %#v, want nil", got)
	}
	plain := filepath.Join(t.TempDir(), "not-a-repo")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if got := SessionPickerWorktreeCWDs(plain, false); len(got) != 1 || got[0] != plain {
		t.Fatalf("disabled worktrees = %#v, want the requested directory", got)
	}
	// An unvalidated checkout falls back to the requested directory alone.
	if got := SessionPickerWorktreeCWDs(plain, true); len(got) != 1 || got[0] != plain {
		t.Fatalf("non-repository expansion = %#v, want the requested directory", got)
	}
}

func TestSessionPickerWorktreeCWDsExpandsLinkedCheckouts(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	primary := t.TempDir()
	runPickerGit(t, primary, "init")
	if err := os.WriteFile(filepath.Join(primary, "file.txt"), []byte("hello\n"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runPickerGit(t, primary, "add", "file.txt")
	runPickerGit(t, primary, "commit", "-m", "initial")
	linked := filepath.Join(t.TempDir(), "linked")
	runPickerGit(t, primary, "worktree", "add", linked, "-b", "linked-branch")

	cwds := SessionPickerWorktreeCWDs(primary, true)
	if len(cwds) < 2 {
		t.Fatalf("expanded cwds = %#v, want the primary and linked checkouts", cwds)
	}
	joined := strings.Join(cwds, "|")
	if !strings.Contains(joined, primary) || !strings.Contains(joined, linked) {
		t.Fatalf("expanded cwds = %#v, want primary %q and linked %q", cwds, primary, linked)
	}
	if got := SessionPickerWorktreeCWDs(primary, false); len(got) != 1 {
		t.Fatalf("disabled expansion = %#v, want one directory", got)
	}
	// Rust #56308: Repo is only offered when multiple canonical linked-worktree
	// directories exist.
	if !SessionPickerRepoFilterAvailable(primary) {
		t.Fatalf("Repo filter should be available for a linked checkout")
	}
	if SessionPickerRepoFilterAvailable(t.TempDir()) {
		t.Fatalf("Repo filter offered for a non-repository directory")
	}
}

// Rust #56308: the Cwd filter is directory-scoped again, the explicit Repo
// filter covers linked worktrees of the same repository, and All ignores the
// directory scope.
func TestSessionPickerFiltersAgainstExpandedWorktreeCWDs(t *testing.T) {
	primary := `D:\repo`
	linked := `D:\repo-linked`
	items := []SessionSummary{
		{ThreadID: "primary", CWD: primary, UpdatedAt: pickerTime(3)},
		{ThreadID: "linked", CWD: linked, UpdatedAt: pickerTime(2)},
		{ThreadID: "other", CWD: `D:\other`, UpdatedAt: pickerTime(1)},
	}
	picker := NewSessionPickerState(SessionPickerResume, items, primary, primary, linked)
	if picker.FilterMode != SessionFilterCWD {
		t.Fatalf("filter mode = %v, want CWD", picker.FilterMode)
	}
	if ids := pickerVisibleIDs(picker); len(ids) != 1 || ids[0] != "primary" {
		t.Fatalf("cwd visible ids = %#v, want primary only", ids)
	}

	// Repo is reached forward from Cwd when the filter is available.
	picker.RepoFilterAvailable = true
	picker.ChangeFilter(SessionFilterNext)
	if picker.FilterMode != SessionFilterRepo {
		t.Fatalf("forward cycle = %v, want Repo", picker.FilterMode)
	}
	if ids := pickerVisibleIDs(picker); len(ids) != 2 || ids[0] != "primary" || ids[1] != "linked" {
		t.Fatalf("repo visible ids = %#v, want primary and linked", ids)
	}
	picker.ChangeFilter(SessionFilterNext)
	if picker.FilterMode != SessionFilterAll {
		t.Fatalf("forward cycle from Repo = %v, want All", picker.FilterMode)
	}
	if got := len(picker.VisibleItems()); got != 3 {
		t.Fatalf("all visible items = %d, want 3", got)
	}
	// Backwards from All lands on Repo, then Cwd.
	picker.ChangeFilter(SessionFilterPrevious)
	if picker.FilterMode != SessionFilterRepo {
		t.Fatalf("backward cycle = %v, want Repo", picker.FilterMode)
	}
	picker.ChangeFilter(SessionFilterPrevious)
	if picker.FilterMode != SessionFilterCWD {
		t.Fatalf("backward cycle from Repo = %v, want Cwd", picker.FilterMode)
	}

	// Without the expanded set the picker keeps the single-directory behavior.
	single := NewSessionPickerState(SessionPickerResume, items, primary)
	if got := len(single.VisibleItems()); got != 1 {
		t.Fatalf("single-directory visible items = %d, want 1", got)
	}
}

func pickerVisibleIDs(picker *SessionPickerState) []string {
	visible := picker.VisibleItems()
	ids := make([]string, 0, len(visible))
	for _, item := range visible {
		ids = append(ids, item.ThreadID)
	}
	return ids
}

func runPickerGit(t *testing.T, dir string, args ...string) {
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

func pickerTime(seconds int64) time.Time {
	return time.Unix(seconds, 0).UTC()
}
