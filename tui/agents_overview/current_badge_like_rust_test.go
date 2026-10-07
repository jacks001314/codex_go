package agentsoverview

import (
	"strings"
	"testing"
)

// Rust #48776 (89bf86d0bd, "Remove the `current` badge from TUI task rows"):
// the agents overview no longer marks the current task's row, so the title
// column keeps the space the badge used to occupy. The counter is Rust's
// `shared_overview_shows_only_root_sessions`, whose rendered-line assertion is
// `"› ● Inspect unnamed task"` with no `current` suffix
// (codex-rs/tui/src/app/agents_overview_tests.rs).
func TestCurrentTaskRowOmitsCurrentBadgeLikeRust(t *testing.T) {
	currentRow := Row{ThreadID: "t-1", Name: "Inspect unnamed task", CWD: "/work/a", Group: GroupWorking, IsCurrent: true}
	plainRow := currentRow
	plainRow.IsCurrent = false

	// The current row is selected here, so selection is identical in both views
	// and the only possible rendering difference is the badge.
	current := strings.Join(New([]Row{currentRow}, "t-1", false).Render(140, 30), "\n")
	plain := strings.Join(New([]Row{plainRow}, "t-1", false).Render(140, 30), "\n")

	if !strings.Contains(current, "● Inspect unnamed task") {
		t.Fatalf("current task row missing from the overview:\n%s", current)
	}
	if strings.Contains(current, "current") {
		t.Fatalf("the current task row still renders the `current` badge:\n%s", current)
	}
	if current != plain {
		t.Fatalf("current and non-current rows render differently; the title column no longer reserves space for the badge:\ncurrent:\n%s\nplain:\n%s", current, plain)
	}
}

// Rust #48776: the row keeps `is_current` for behaviour (the overview selects
// that row by default, Rust agents_overview_view.rs `position(|row|
// row.is_current)`), so dropping the badge must not change which row is
// selected when the requested thread is absent from the list.
func TestCurrentTaskRowStillDrivesDefaultSelectionLikeRust(t *testing.T) {
	rows := []Row{
		{ThreadID: "t-1", Name: "alpha", CWD: "/work/a", Group: GroupWorking},
		{ThreadID: "t-2", Name: "beta", CWD: "/work/a", Group: GroupWorking, IsCurrent: true},
	}
	view := New(rows, "missing-thread", false)
	selected := view.SelectedRow()
	if selected == nil || selected.ThreadID != "t-2" {
		t.Fatalf("selected row = %#v, want the current task t-2", selected)
	}
}
