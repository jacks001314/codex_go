package agentsoverview

import "testing"

func TestHideSelectedHidesRowAndMovesSelectionLikeRust(t *testing.T) {
	view := New(sampleRows(), "", false)
	if view.Selected != 0 {
		t.Fatalf("initial selection = %d, want 0", view.Selected)
	}
	if action := view.HideSelected(); action != ActionHideThread {
		t.Fatalf("HideSelected() = %v, want ActionHideThread", action)
	}
	if !view.isHidden("t-1") {
		t.Fatal("t-1 should be hidden")
	}
	if got := len(view.VisibleIndices()); got != 3 {
		t.Fatalf("visible rows after hide = %d, want 3", got)
	}
	if view.Selected != 1 {
		t.Fatalf("selection after hide = %d, want next visible row 1", view.Selected)
	}
	if row := view.SelectedRow(); row == nil || row.ThreadID != "t-2" {
		t.Fatalf("selected row after hide = %#v, want t-2", row)
	}
	// Hiding hides only the selected row, not its group.
	if got := len(view.Rows); got != 4 {
		t.Fatalf("rows mutated by hide = %d, want 4", got)
	}
}

func TestHideSelectedWithoutRowsIsNoop(t *testing.T) {
	var view View
	if action := view.HideSelected(); action != ActionNone {
		t.Fatalf("HideSelected() on empty view = %v, want ActionNone", action)
	}
}

func TestHiddenRowsSurviveRefreshAndUnhideOnResume(t *testing.T) {
	view := New(sampleRows(), "", false)
	view.HideSelected() // hides t-1

	// Activity/metadata refreshes must not reveal a hidden root.
	view.ApplyRefresh(sampleRows(), "t-1")
	if !view.isHidden("t-1") {
		t.Fatal("hidden thread revealed by ApplyRefresh")
	}
	if got := len(view.VisibleIndices()); got != 3 {
		t.Fatalf("visible rows after refresh = %d, want 3", got)
	}
	if view.SelectedThreadID() == "t-1" {
		t.Fatalf("selection restored onto hidden thread")
	}

	view.UnhideThread("t-1")
	if view.isHidden("t-1") {
		t.Fatal("UnhideThread left the thread hidden")
	}
	if got := len(view.VisibleIndices()); got != 4 {
		t.Fatalf("visible rows after unhide = %d, want 4", got)
	}
}

func TestHiddenThreadsRoundTripAcrossReopen(t *testing.T) {
	view := New(sampleRows(), "", false)
	view.HideSelected()
	saved := view.HiddenThreads()
	if len(saved) != 1 {
		t.Fatalf("HiddenThreads() = %#v, want one entry", saved)
	}

	reopened := New(sampleRows(), "", false)
	reopened.SetHiddenThreads(saved)
	if !reopened.isHidden("t-1") {
		t.Fatal("reopened dashboard revealed a hidden thread")
	}
	if got := len(reopened.VisibleIndices()); got != 3 {
		t.Fatalf("reopened visible rows = %d, want 3", got)
	}
	// The returned map is a copy; mutating it must not affect the source view.
	delete(saved, "t-1")
	if !view.isHidden("t-1") {
		t.Fatal("HiddenThreads() returned an aliased map")
	}
}

func TestHiddenRowsAreExcludedBeforeSearchFiltering(t *testing.T) {
	view := New(sampleRows(), "", false)
	view.State.Search = "alpha"
	view.State.Searching = true
	view.fitSelection()
	if got := len(view.VisibleIndices()); got != 1 {
		t.Fatalf("search-visible rows = %d, want 1", got)
	}
	view.HideSelected() // hides the search match
	if got := len(view.VisibleIndices()); got != 0 {
		t.Fatalf("visible rows after hiding the only search match = %d, want 0", got)
	}
}
