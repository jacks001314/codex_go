package tui

import (
	"reflect"
	"testing"
)

func TestSessionArchiveSuccessMessageMatchesRust(t *testing.T) {
	cases := []struct {
		action SessionArchiveAction
		id     string
		name   string
		want   string
	}{
		{SessionArchive, "thread-1", "", "Archived session thread-1."},
		{SessionArchive, "thread-1", "Work", "Archived session Work (thread-1)."},
		{SessionDelete, "thread-2", "Old", "Deleted session Old (thread-2)."},
		{SessionUnarchive, "thread-3", "Back", "Unarchived session Back (thread-3)."},
	}
	for _, tc := range cases {
		if got := SessionArchiveSuccessMessage(tc.action, tc.id, tc.name); got != tc.want {
			t.Fatalf("SuccessMessage(%s) = %q, want %q", tc.action, got, tc.want)
		}
	}
}

func TestSessionArchiveSearchScopeMatchesRust(t *testing.T) {
	scope, archived := SessionArchiveSearchScope(SessionArchive)
	if scope != "active" || !reflect.DeepEqual(archived, []bool{false}) {
		t.Fatalf("archive scope = %q %#v", scope, archived)
	}
	scope, archived = SessionArchiveSearchScope(SessionDelete)
	if scope != "active or archived" || !reflect.DeepEqual(archived, []bool{false, true}) {
		t.Fatalf("delete scope = %q %#v", scope, archived)
	}
	scope, archived = SessionArchiveSearchScope(SessionUnarchive)
	if scope != "archived" || !reflect.DeepEqual(archived, []bool{true}) {
		t.Fatalf("unarchive scope = %q %#v", scope, archived)
	}
}

func TestSessionArchiveDeletePromptHelpersMatchRust(t *testing.T) {
	if !SessionDeleteNeedsPrompt(SessionDelete, DeleteConfirmationPrompt) {
		t.Fatal("delete prompt should need confirmation")
	}
	if SessionDeleteNeedsPrompt(SessionDelete, DeleteConfirmationSkip) {
		t.Fatal("delete --force should skip confirmation")
	}
	if SessionDeleteNeedsPrompt(SessionArchive, DeleteConfirmationPrompt) {
		t.Fatal("archive should not need delete confirmation")
	}
	for _, yes := range []string{"y", "Y", "yes", " YES "} {
		if !ConfirmSessionDeleteAnswer(yes) {
			t.Fatalf("%q should confirm deletion", yes)
		}
	}
	for _, no := range []string{"", "n", "no", "sure"} {
		if ConfirmSessionDeleteAnswer(no) {
			t.Fatalf("%q should not confirm deletion", no)
		}
	}
	lines := SessionDeletePromptLines(ResolvedSessionTarget{SessionID: "thread-1", SessionName: "Work"})
	if len(lines) != 3 || lines[0] != "Permanently delete session 'Work' (thread-1)?" || lines[1] != "This cannot be undone. Subagent threads will also be deleted." {
		t.Fatalf("prompt lines = %#v", lines)
	}
	if got := SessionArchiveNoMatchMessage(SessionUnarchive, "missing"); got != "No archived session found matching 'missing'." {
		t.Fatalf("no-match = %q", got)
	}
	if got := SessionArchiveCancelledMessage(SessionDelete); got != "Delete cancelled." {
		t.Fatalf("cancelled = %q", got)
	}
}
