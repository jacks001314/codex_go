package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/worktree"
)

func TestWorktreeOwnerTitleFallbackAndTruncationLikeRust(t *testing.T) {
	if got := WorktreeOwnerTitle(WorktreeOwnerLookup{Name: "  Fix the parser  ", Preview: "ignored"}); got != "Fix the parser" {
		t.Fatalf("title = %q, want trimmed name", got)
	}
	if got := WorktreeOwnerTitle(WorktreeOwnerLookup{Preview: "\n\n  First preview line\nsecond"}); got != "First preview line" {
		t.Fatalf("title = %q, want first non-empty preview line", got)
	}
	if got := WorktreeOwnerTitle(WorktreeOwnerLookup{Preview: "  \n\t"}); got != "Untitled conversation" {
		t.Fatalf("title = %q, want Untitled conversation", got)
	}

	long := strings.Repeat("\u00e9", 81)
	got := WorktreeOwnerTitle(WorktreeOwnerLookup{Name: long})
	runes := []rune(got)
	if len(runes) != worktreeOwnerTitleMaxChars || runes[len(runes)-1] != '\u2026' {
		t.Fatalf("title = %q, want 79 runes plus ellipsis", got)
	}
	if string(runes[:len(runes)-1]) != strings.Repeat("\u00e9", 79) {
		t.Fatalf("truncated title = %q, want rune-safe prefix", got)
	}

	exact := strings.Repeat("a", worktreeOwnerTitleMaxChars)
	if got := WorktreeOwnerTitle(WorktreeOwnerLookup{Name: exact}); got != exact {
		t.Fatalf("80-character title = %q, want unchanged", got)
	}
}

func TestWorktreeUpdatedAgoLikeRust(t *testing.T) {
	const now int64 = 10_000_000
	for _, test := range []struct {
		updatedAt int64
		want      string
	}{
		{now, "just now"},
		{now - 59, "just now"},
		{now - 60, "1m ago"},
		{now - 3_599, "59m ago"},
		{now - 3_600, "1h ago"},
		{now - 86_399, "23h ago"},
		{now - 86_400, "1d ago"},
		{now + 500, "just now"},
	} {
		if got := WorktreeUpdatedAgo(test.updatedAt, now); got != test.want {
			t.Fatalf("WorktreeUpdatedAgo(%d) = %q, want %q", test.updatedAt, got, test.want)
		}
	}
}

func TestWorktreeBrowserRowLikeRust(t *testing.T) {
	const now int64 = 10_000_000
	summary := &WorktreeThreadSummary{ID: "thread-1", Title: "Fix the parser", UpdatedAt: now - 120}

	name, description, search := WorktreeBrowserRow(WorktreeBrowserEntry{CWD: "/pool/wt", Owner: WorktreeOwner{Kind: WorktreeOwnerNone}}, now)
	if name != "/pool/wt" || description != "No attached thread" || search != "/pool/wt /pool/wt" {
		t.Fatalf("none row = %q, %q, %q", name, description, search)
	}

	name, description, _ = WorktreeBrowserRow(WorktreeBrowserEntry{CWD: "/pool/wt", Owner: WorktreeOwner{Kind: WorktreeOwnerUnavailable, ThreadID: "thread-1"}}, now)
	if name != "/pool/wt" || description != "Owner thread unavailable" {
		t.Fatalf("unavailable row = %q, %q", name, description)
	}

	name, description, search = WorktreeBrowserRow(WorktreeBrowserEntry{CWD: "/pool/wt", Owner: WorktreeOwner{Kind: WorktreeOwnerResumable, ThreadID: "thread-1", Summary: summary}}, now)
	if name != "Fix the parser" || description != "updated 2m ago \u00b7 /pool/wt" || search != "Fix the parser /pool/wt" {
		t.Fatalf("resumable row = %q, %q, %q", name, description, search)
	}

	_, description, _ = WorktreeBrowserRow(WorktreeBrowserEntry{CWD: "/pool/wt", Owner: WorktreeOwner{Kind: WorktreeOwnerArchived, ThreadID: "thread-1", Summary: summary}}, now)
	if description != "Archived \u00b7 updated 2m ago \u00b7 /pool/wt" {
		t.Fatalf("archived description = %q", description)
	}
}

func TestResolveWorktreeOwnerSummariesLikeRust(t *testing.T) {
	entries := []WorktreeBrowserEntry{
		{Root: "/pool/a", CWD: "/pool/a", Owner: WorktreeOwner{Kind: WorktreeOwnerNone}},
		{Root: "/pool/b", CWD: "/pool/b", Owner: WorktreeOwner{Kind: WorktreeOwnerUnavailable, ThreadID: "thread-b"}},
		{Root: "/pool/c", CWD: "/pool/c", Owner: WorktreeOwner{Kind: WorktreeOwnerUnavailable, ThreadID: "thread-c"}},
		{Root: "/pool/d", CWD: "/pool/d", Owner: WorktreeOwner{Kind: WorktreeOwnerUnavailable, ThreadID: "thread-d"}},
	}
	resolved := ResolveWorktreeOwnerSummaries(entries, func(threadID string) (WorktreeOwnerLookup, bool) {
		switch threadID {
		case "thread-b":
			return WorktreeOwnerLookup{ID: threadID, Name: "Live owner", UpdatedAt: 5}, true
		case "thread-c":
			return WorktreeOwnerLookup{ID: threadID, Preview: "Archived preview", UpdatedAt: 7, Archived: true}, true
		default:
			return WorktreeOwnerLookup{}, false
		}
	})
	if resolved[0].Owner.Kind != WorktreeOwnerNone {
		t.Fatalf("none entry kind = %v, want unchanged", resolved[0].Owner.Kind)
	}
	if resolved[1].Owner.Kind != WorktreeOwnerResumable ||
		resolved[1].Owner.Summary == nil || resolved[1].Owner.Summary.Title != "Live owner" {
		t.Fatalf("resumable entry = %#v", resolved[1].Owner)
	}
	if resolved[2].Owner.Kind != WorktreeOwnerArchived ||
		resolved[2].Owner.Summary == nil || resolved[2].Owner.Summary.Title != "Archived preview" {
		t.Fatalf("archived entry = %#v", resolved[2].Owner)
	}
	if resolved[3].Owner.Kind != WorktreeOwnerUnavailable {
		t.Fatalf("unresolved entry kind = %v, want unavailable", resolved[3].Owner.Kind)
	}

	// Resolution mutates the entries in place (as Rust's fetch does), so use a
	// fresh list for the nil-lookup check.
	unchanged := ResolveWorktreeOwnerSummaries([]WorktreeBrowserEntry{
		{Root: "/pool/b", CWD: "/pool/b", Owner: WorktreeOwner{Kind: WorktreeOwnerUnavailable, ThreadID: "thread-b"}},
	}, nil)
	if unchanged[0].Owner.Kind != WorktreeOwnerUnavailable {
		t.Fatalf("nil lookup kind = %v, want unavailable", unchanged[0].Owner.Kind)
	}
}

func TestWorktreeBrowserActionItemsLikeRust(t *testing.T) {
	resumable := WorktreeBrowserEntry{
		Root:  "/pool/wt",
		CWD:   "/pool/wt/sub",
		Owner: WorktreeOwner{Kind: WorktreeOwnerResumable, ThreadID: "thread-1", Summary: &WorktreeThreadSummary{ID: "thread-1", Title: "Owner"}},
	}
	items := WorktreeBrowserActionItems(resumable, "/src")
	if len(items) != 3 || items[0].Name != "Resume owner thread" || items[0].Action.ThreadID != "thread-1" {
		t.Fatalf("resumable actions = %#v", items)
	}
	if items[1].Name != "Copy working directory" || items[1].Action.Path != "/pool/wt/sub" {
		t.Fatalf("copy action = %#v", items[1])
	}
	if items[2].Name != "Delete worktree" || items[2].Disabled || items[2].Action.Path != "/pool/wt" {
		t.Fatalf("delete action = %#v", items[2])
	}

	archived := resumable
	archived.Owner.Kind = WorktreeOwnerArchived
	if got := WorktreeBrowserActionItems(archived, "/src"); len(got) != 2 || got[0].Name != "Copy working directory" {
		t.Fatalf("archived actions = %#v, want copy + delete only", got)
	}

	// A session inside the worktree cannot delete it.
	inside := WorktreeBrowserActionItems(resumable, "/pool/wt/sub/dir")
	deleteItem := inside[len(inside)-1]
	if !deleteItem.Disabled || deleteItem.DisabledReason != "Switch to another checkout before deleting this one" {
		t.Fatalf("delete inside = %#v, want disabled", deleteItem)
	}
	// A sibling checkout sharing a prefix is not inside the worktree.
	sibling := WorktreeBrowserActionItems(resumable, "/pool/wt2")
	if sibling[len(sibling)-1].Disabled {
		t.Fatalf("sibling delete = %#v, want enabled", sibling[len(sibling)-1])
	}
	if got := WorktreeActionsTitle(resumable); got != "Worktree: Owner" {
		t.Fatalf("actions title = %q", got)
	}
}

func TestWorktreeDeleteConfirmationItemsLikeRust(t *testing.T) {
	items := WorktreeDeleteConfirmationItems("/pool/wt")
	if len(items) != 2 || items[0].Name != "Cancel" || items[0].Disabled {
		t.Fatalf("confirmation items = %#v, want Cancel first", items)
	}
	if items[1].Name != "Delete worktree" ||
		items[1].Description != "Keeps thread history; may disrupt other sessions" ||
		items[1].Action.Kind != WorktreeActionRemove || items[1].Action.Path != "/pool/wt" {
		t.Fatalf("delete item = %#v", items[1])
	}
}

func TestRemoveManagedWorktreeRequiresSettings(t *testing.T) {
	if err := RemoveManagedWorktree(worktree.WorktreeSettings{}, "", ""); err == nil {
		t.Fatal("RemoveManagedWorktree with empty settings should fail")
	}
}

// Mirrors Rust worktree_browser::list: the pool's managed checkouts are listed
// with a recorded owner thread id classified as unavailable until its summary
// is resolved.
func TestListManagedWorktreeEntriesLikeRust(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	source := t.TempDir()
	runGitForWorktreeBrowserTest(t, git, source, "init")
	runGitForWorktreeBrowserTest(t, git, source, "config", "user.email", "test@example.com")
	runGitForWorktreeBrowserTest(t, git, source, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(source, "file.txt"), []byte("hello\n"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runGitForWorktreeBrowserTest(t, git, source, "add", "file.txt")
	runGitForWorktreeBrowserTest(t, git, source, "commit", "-m", "initial")

	settings, err := worktree.FromDesktopConfig(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("FromDesktopConfig() error = %v", err)
	}
	manager := worktree.NewWorktreeManager(settings)
	created, err := manager.Create(source, "")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := manager.BindThread(created.Root, "thread-1"); err != nil {
		t.Fatalf("BindThread() error = %v", err)
	}

	entries, err := ListManagedWorktreeEntries(settings, source)
	if err != nil {
		t.Fatalf("ListManagedWorktreeEntries() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %#v, want one managed worktree", entries)
	}
	if entries[0].Root != created.Root || entries[0].CWD != created.CWD {
		t.Fatalf("entry = %#v, want %#v", entries[0], created)
	}
	if entries[0].Owner.Kind != WorktreeOwnerUnavailable || entries[0].Owner.ThreadID != "thread-1" {
		t.Fatalf("owner = %#v, want unavailable thread-1", entries[0].Owner)
	}
}

func runGitForWorktreeBrowserTest(t *testing.T, git string, dir string, args ...string) {
	t.Helper()
	command := exec.Command(git, args...)
	command.Dir = dir
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}
