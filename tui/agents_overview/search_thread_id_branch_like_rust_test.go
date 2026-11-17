package agentsoverview

import "testing"

// Rust #61926: agent search also matches case-insensitive substrings of thread
// IDs and Git branch names.
func TestAgentsOverviewSearchMatchesThreadIDAndBranchLikeRust(t *testing.T) {
	view := New([]Row{
		{
			ThreadID:  "abcdef01-0000-4000-8000-000000000042",
			Name:      "Task to find",
			CWD:       "/work/repo",
			GitBranch: "Fix/Resume",
			Group:     GroupReady,
		},
		{
			ThreadID:  "11111111-0000-4000-8000-000000000001",
			Name:      "Unrelated",
			CWD:       "/work/repo",
			GitBranch: "main",
			Group:     GroupReady,
		},
	}, "", false)

	for _, query := range []string{"FIX/RES", "ABCDEF", "TASK TO FIND", "000042"} {
		view.State.Search = query
		if got := visibleFilterThreads(view); got != "abcdef01-0000-4000-8000-000000000042" {
			t.Fatalf("search %q = %q", query, got)
		}
	}
	view.State.Search = "MAIN"
	if got := visibleFilterThreads(view); got != "11111111-0000-4000-8000-000000000001" {
		t.Fatalf("branch-only search = %q", got)
	}
	view.State.Search = "missing"
	if got := visibleFilterThreads(view); got != "" {
		t.Fatalf("non-matching search = %q", got)
	}
}
