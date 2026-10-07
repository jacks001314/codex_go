package agentsoverview

import "testing"

func rowsWithIsCurrent(threadID string) []Row {
	rows := sampleRows()
	for i := range rows {
		if rows[i].ThreadID == threadID {
			rows[i].IsCurrent = true
		}
	}
	return rows
}

func rowsWithout(threadIDs ...string) []Row {
	skip := map[string]struct{}{}
	for _, id := range threadIDs {
		skip[id] = struct{}{}
	}
	out := []Row{}
	for _, row := range sampleRows() {
		if _, ok := skip[row.ThreadID]; ok {
			continue
		}
		out = append(out, row)
	}
	return out
}

func removedSet(threadIDs ...string) map[string]struct{} {
	removed := map[string]struct{}{}
	for _, id := range threadIDs {
		removed[id] = struct{}{}
	}
	return removed
}

// Rust #50505 (47379efd52) AgentsOverviewView::selection_after_removal: pick the
// next surviving task in displayed order, fall back to the previous surviving
// task, and report nothing when the whole displayed list is removed.
// Rust test: agents_overview_actions_tests.rs::archiving_selects_the_next_displayed_task
// (its selections snapshot walks Task 3 -> Other 2 -> Task 1 -> Task 4).
func TestSelectionAfterRemovalPicksNextThenPreviousLikeRust(t *testing.T) {
	view := New(sampleRows(), "t-2", false)
	if got := view.SelectedThreadID(); got != "t-2" {
		t.Fatalf("initial selection = %q, want t-2", got)
	}
	for _, testCase := range []struct {
		name    string
		removed []string
		want    string
	}{
		{"next displayed task", []string{"t-2"}, "t-3"},
		{"next after a batched removal", []string{"t-2", "t-3"}, "t-4"},
		{"previous when nothing follows", []string{"t-2", "t-3", "t-4"}, "t-1"},
		{"empty list", []string{"t-1", "t-2", "t-3", "t-4"}, ""},
	} {
		if got := view.SelectionAfterRemoval(removedSet(testCase.removed...)); got != testCase.want {
			t.Fatalf("%s: SelectionAfterRemoval(%v) = %q, want %q", testCase.name, testCase.removed, got, testCase.want)
		}
	}

	// The scan walks the filtered display order: a search that leaves no other
	// task returns nothing, and an anchor that filtering hid is not a start
	// point at all (Rust returns None when the selection is not displayed).
	filtered := New(sampleRows(), "t-3", false)
	filtered.State.Search = "gamma"
	if got := filtered.SelectionAfterRemoval(removedSet("t-3")); got != "" {
		t.Fatalf("filtered SelectionAfterRemoval = %q, want empty", got)
	}
	filtered.State.Search = "beta"
	if got := filtered.SelectionAfterRemoval(removedSet("t-3")); got != "" {
		t.Fatalf("hidden-anchor SelectionAfterRemoval = %q, want empty", got)
	}
}

// Rust #50505 prepare_agents_overview_removal / selection_after_removal: a
// pending successor survives duplicate and unrelated removal notifications, and
// advances again when the removal set catches up with it.
func TestPrepareRemovalSurvivesBatchedAndDuplicateRemovalsLikeRust(t *testing.T) {
	view := New(sampleRows(), "t-2", false)
	view.PrepareRemoval(removedSet("t-2"))
	if got := view.PendingSelectionAfterRemoval(); got != "t-3" {
		t.Fatalf("pending after first removal = %q, want t-3", got)
	}
	// A duplicate notification for an unrelated task keeps the successor.
	view.PrepareRemoval(removedSet("t-1"))
	if got := view.PendingSelectionAfterRemoval(); got != "t-3" {
		t.Fatalf("pending after unrelated removal = %q, want t-3", got)
	}
	// When the successor is removed too, the scan advances to the next survivor.
	view.PrepareRemoval(removedSet("t-3"))
	if got := view.PendingSelectionAfterRemoval(); got != "t-4" {
		t.Fatalf("pending after successor removal = %q, want t-4", got)
	}

	// Removing a task that is not the anchor is a no-op.
	other := New(sampleRows(), "t-1", false)
	other.PrepareRemoval(removedSet("t-4"))
	if got := other.PendingSelectionAfterRemoval(); got != "" {
		t.Fatalf("pending for an unselected removal = %q, want empty", got)
	}
}

// Rust #50505: the view rebuild consumes the pending successor, so the archived
// or deleted task cannot drag the selection to the top of the list.
func TestApplyRefreshUsesPendingSuccessorAfterRemovalLikeRust(t *testing.T) {
	view := New(sampleRows(), "t-2", false)
	view.PrepareRemoval(removedSet("t-2"))
	view.ApplyRefresh(rowsWithout("t-2"), view.SelectedThreadID())
	if got := view.SelectedThreadID(); got != "t-3" {
		t.Fatalf("selection after removal refresh = %q, want t-3", got)
	}
	if got := view.PendingSelectionAfterRemoval(); got != "" {
		t.Fatalf("pending successor was not consumed: %q", got)
	}
	// Later repaints keep the successor because it is the live selection now.
	view.ApplyRefresh(rowsWithout("t-2"), view.SelectedThreadID())
	if got := view.SelectedThreadID(); got != "t-3" {
		t.Fatalf("selection after second repaint = %q, want t-3", got)
	}
}

// A removal the host cannot observe in advance (a server-side delete) still
// keeps the selection adjacent instead of jumping to the first displayed task
// (the Rust app calls prepare_agents_overview_removal from
// remove_agents_overview_thread for these notifications).
func TestApplyRefreshKeepsSelectionAdjacentForUnpreparedRemovalLikeRust(t *testing.T) {
	view := New(sampleRows(), "t-2", false)
	view.ApplyRefresh(rowsWithout("t-2"), "t-2")
	if got := view.SelectedThreadID(); got != "t-3" {
		t.Fatalf("selection after external removal = %q, want t-3", got)
	}
	// The attached task is the documented fallback when the removal empties the
	// displayed list.
	current := New(rowsWithIsCurrent("t-3"), "t-3", false)
	current.ApplyRefresh([]Row{current.Rows[2]}, "t-1")
	if got := current.SelectedThreadID(); got != "t-3" {
		t.Fatalf("selection after unmatched rebuild = %q, want t-3", got)
	}
}

// Rust #50505: hiding the last displayed task selects the previous one, matching
// the archive/delete successor rules. Rust test:
// agents_overview_actions_tests.rs::external_removals_preserve_adjacent_selection.
func TestHideSelectedKeepsAdjacentSelectionAtListEndLikeRust(t *testing.T) {
	view := New(sampleRows(), "t-4", false)
	if action := view.HideSelected(); action != ActionHideThread {
		t.Fatalf("HideSelected() = %v, want ActionHideThread", action)
	}
	if got := view.SelectedThreadID(); got != "t-3" {
		t.Fatalf("selection after hiding the last row = %q, want t-3", got)
	}
	if got := view.PendingSelectionAfterRemoval(); got != "t-3" {
		t.Fatalf("pending successor after hide = %q, want t-3", got)
	}
	// The pending successor also drives the next rebuild.
	view.ApplyRefresh(rowsWithout("t-4"), view.SelectedThreadID())
	if got := view.SelectedThreadID(); got != "t-3" {
		t.Fatalf("selection after hide refresh = %q, want t-3", got)
	}
}

// Rust #50505: a requested task that is no longer displayed falls back to the
// attached task before defaulting to the first row (the Rust view's
// `or_else(is_current)` in agents_overview_view.rs::new).
func TestNewFallsBackToCurrentTaskLikeRust(t *testing.T) {
	view := New(rowsWithIsCurrent("t-3"), "gone", false)
	if got := view.SelectedThreadID(); got != "t-3" {
		t.Fatalf("fallback selection = %q, want t-3", got)
	}
	if got := New(sampleRows(), "gone", false).SelectedThreadID(); got != "t-1" {
		t.Fatalf("selection without a current task = %q, want t-1", got)
	}
}
