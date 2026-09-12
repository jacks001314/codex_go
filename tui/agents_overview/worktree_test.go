package agentsoverview

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitWorktreeFixture(t *testing.T) (primary string, linked string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	primary = t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"add", "-A"},
		{"commit", "-m", "initial"},
	} {
		if args[0] == "add" {
			if err := os.WriteFile(filepath.Join(primary, "file.txt"), []byte("hello\n"), 0o600); err != nil {
				t.Fatalf("write file: %v", err)
			}
		}
		runOverviewGit(t, primary, args...)
	}
	if err := os.MkdirAll(filepath.Join(primary, "pkg"), 0o755); err != nil {
		t.Fatalf("mkdir pkg: %v", err)
	}
	linked = filepath.Join(t.TempDir(), "linked")
	runOverviewGit(t, primary, "worktree", "add", linked, "-b", "linked-branch")
	if err := os.MkdirAll(filepath.Join(linked, "pkg"), 0o755); err != nil {
		t.Fatalf("mkdir linked pkg: %v", err)
	}
	return primary, linked
}

func runOverviewGit(t *testing.T, dir string, args ...string) {
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

func TestLinkedWorktreeRowsGroupTogetherWhenEnabled(t *testing.T) {
	primary, linked := gitWorktreeFixture(t)
	rows := []Row{
		{ThreadID: "primary", CWD: filepath.Join(primary, "pkg"), Group: GroupReady},
		{ThreadID: "linked", CWD: filepath.Join(linked, "pkg"), Group: GroupReady},
	}

	grouped := New(rows, "", false)
	grouped.SetWorktreesEnabled(true)
	if !grouped.projectGroupAt(0).equal(grouped.projectGroupAt(1)) {
		t.Fatalf("linked checkouts should share a project group: %#v vs %#v",
			grouped.projectGroupAt(0), grouped.projectGroupAt(1))
	}
	wantHeading := filepath.Join(primary, "pkg")
	if got := grouped.projectGroupAt(1).heading; got != wantHeading {
		t.Fatalf("group heading = %q, want the primary checkout's directory %q", got, wantHeading)
	}
	lines := strings.Join(grouped.Render(400, 24), "\n")
	if !strings.Contains(lines, wantHeading) {
		t.Fatalf("rendered dashboard missing the combined group heading:\n%s", lines)
	}
	if strings.Contains(lines, filepath.Join(linked, "pkg")) {
		t.Fatalf("combined group should use the primary heading, not the linked checkout:\n%s", lines)
	}

	// Without the feature the two checkouts stay in separate groups.
	separate := New(rows, "", false)
	if separate.projectGroupAt(0).equal(separate.projectGroupAt(1)) {
		t.Fatal("checkouts must not share a group when worktrees are disabled")
	}
	separateLines := strings.Join(separate.Render(400, 24), "\n")
	if !strings.Contains(separateLines, filepath.Join(linked, "pkg")) {
		t.Fatalf("disabled grouping should render the linked checkout heading:\n%s", separateLines)
	}
}

func TestApplyRefreshPreservesWorktreesGrouping(t *testing.T) {
	primary, linked := gitWorktreeFixture(t)
	view := New(nil, "", false)
	view.SetWorktreesEnabled(true)
	view.ApplyRefresh([]Row{
		{ThreadID: "primary", CWD: filepath.Join(primary, "pkg"), Group: GroupReady},
		{ThreadID: "linked", CWD: filepath.Join(linked, "pkg"), Group: GroupReady},
	}, "")
	if !view.projectGroupAt(0).equal(view.projectGroupAt(1)) {
		t.Fatal("ApplyRefresh dropped the linked-checkout grouping")
	}
}
