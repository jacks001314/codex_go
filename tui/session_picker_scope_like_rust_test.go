package tui

import "testing"

// TestSessionPickerRepoFilterAcceptsLateWorktreeSetLikeRust covers the picker
// receiving linked-checkout discovery after it already filtered rows: Rust sets
// repo_filter_available and clears local_cwd_matches, so the next filter pass
// sees the expanded scope.
func TestSessionPickerRepoFilterAcceptsLateWorktreeSetLikeRust(t *testing.T) {
	picker := NewSessionPickerState(SessionPickerResume, []SessionSummary{
		{ThreadID: "thread-main", CWD: "/repo"},
		{ThreadID: "thread-linked", CWD: "/repo-wt"},
	}, "/repo")
	picker.RepoFilterAvailable = true
	picker.FilterMode = SessionFilterRepo

	if scope := picker.ListingScope(); scope.All || len(scope.CWDs) != 1 || scope.CWDs[0] != "/repo" {
		t.Fatalf("listing scope before discovery = %#v", scope)
	}
	if got := len(picker.VisibleItems()); got != 1 {
		t.Fatalf("rows before discovery = %d, want only the filter directory", got)
	}

	picker.FilterCWDs = []string{"/repo", "/repo-wt"}
	if scope := picker.ListingScope(); scope.All || len(scope.CWDs) != 2 {
		t.Fatalf("listing scope after discovery = %#v", scope)
	}
	if got := len(picker.VisibleItems()); got != 2 {
		t.Fatalf("rows after discovery = %d, want the linked checkout too", got)
	}
}
