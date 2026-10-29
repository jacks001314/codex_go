package agentsoverview

import "testing"

// Mirrors Rust #45978: hiding the selected task selects the next visible task
// in displayed order, falling back to the previous task at the end of the list.
func TestHideSelectedKeepsSelectionAdjacentLikeRust(t *testing.T) {
	view := New(sampleRows(), "", false)
	visible := view.VisibleIndices()
	if len(visible) < 3 {
		t.Fatalf("visible rows = %d, want at least 3", len(visible))
	}
	view.Selected = visible[1]
	wantNext := view.Rows[visible[2]].ThreadID
	if action := view.HideSelected(); action != ActionHideThread {
		t.Fatalf("HideSelected() = %v, want ActionHideThread", action)
	}
	if got := view.SelectedThreadID(); got != wantNext {
		t.Fatalf("selection after hiding a middle row = %q, want next displayed row %q", got, wantNext)
	}

	atEnd := New(sampleRows(), "", false)
	lastPosition := len(atEnd.VisibleIndices()) - 1
	atEnd.Selected = atEnd.VisibleIndices()[lastPosition]
	wantPrevious := atEnd.Rows[atEnd.VisibleIndices()[lastPosition-1]].ThreadID
	atEnd.HideSelected()
	if got := atEnd.SelectedThreadID(); got != wantPrevious {
		t.Fatalf("selection after hiding the last row = %q, want previous displayed row %q", got, wantPrevious)
	}
}

// Mirrors Rust #45978: the adjacent task follows the active search filter, so a
// filtered list does not fall back to a hidden or non-matching row.
func TestHideSelectedFollowsSearchFilterLikeRust(t *testing.T) {
	view := New([]Row{
		{ThreadID: "a", Preview: "match one", Group: GroupReady},
		{ThreadID: "b", Preview: "other", Group: GroupReady},
		{ThreadID: "c", Preview: "match two", Group: GroupReady},
	}, "", false)
	view.State.Search = "match"
	view.State.Searching = true
	view.fitSelection()
	if view.SelectedThreadID() != "a" {
		t.Fatalf("initial search selection = %q, want a", view.SelectedThreadID())
	}
	view.HideSelected()
	if got := view.SelectedThreadID(); got != "c" {
		t.Fatalf("selection after hiding a search match = %q, want c", got)
	}
}

// Mirrors Rust #45978: hiding a rename target cancels the draft instead of
// carrying it to the neighboring task.
func TestHideSelectedWhileRenamingCancelsDraftLikeRust(t *testing.T) {
	view := New(sampleRows(), "", false)
	view.State.Renaming = true
	view.State.RenameTarget = "t-1"
	view.State.Input = "draft"
	view.Selected = 0
	view.HideSelected()
	if view.State.Renaming || view.State.Input != "" {
		t.Fatalf("rename state after hiding the target = %#v, want the draft cancelled", view.State)
	}
	if row := view.SelectedRow(); row != nil && row.ThreadID == "t-1" {
		t.Fatal("selection stayed on the hidden rename target")
	}
}

// Mirrors Rust #45978: hiding every visible task leaves an empty list without
// panicking and without resurrecting a hidden row.
func TestHideSelectedHidesEveryVisibleTaskLikeRust(t *testing.T) {
	view := New([]Row{{ThreadID: "only", Group: GroupReady}}, "", false)
	view.HideSelected()
	if visible := view.VisibleIndices(); len(visible) != 0 {
		t.Fatalf("visible rows after hiding the only task = %v, want none", visible)
	}
	if view.SelectedRow() != nil {
		t.Fatalf("selected row = %#v, want none", view.SelectedRow())
	}
}
