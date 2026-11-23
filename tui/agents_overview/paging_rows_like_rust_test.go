package agentsoverview

import "testing"

func pagingRows() []Row {
	return []Row{
		{ThreadID: "a1", Name: "a1", CWD: "/work/a", Group: GroupWorking, UpdatedAt: 8},
		{ThreadID: "a2", Name: "a2", CWD: "/work/a", Group: GroupWorking, UpdatedAt: 7},
		{ThreadID: "b1", Name: "b1", CWD: "/work/b", Group: GroupReady, UpdatedAt: 6},
		{ThreadID: "b2", Name: "b2", CWD: "/work/b", Group: GroupReady, UpdatedAt: 5},
		{ThreadID: "c1", Name: "c1", CWD: "/work/c", Group: GroupFinished, UpdatedAt: 4},
	}
}

func selectedThread(view *View) string {
	return view.Rows[view.Selected].ThreadID
}

// Mirrors Rust #63145: paging counts the rows the list actually paints, so a
// group heading and its gap consume viewport rows instead of silently hiding
// tasks from the next page.
func TestAgentsOverviewPageDownCountsDisplayedRowsLikeRust(t *testing.T) {
	view := New(pagingRows(), "", false)
	if view.State.Grouping != GroupingProject {
		t.Fatalf("grouping = %v, want project", view.State.Grouping)
	}
	view.pageHeight = 4
	view.Selected = 0
	view.PageDown()
	// The first page paints heading + a1 + a2 + gap, so it shows two tasks and
	// the next page key lands on the second task after the selection.
	if got := selectedThread(view); got != "b1" {
		t.Fatalf("PageDown from a1 selected %q, want b1 (two displayed tasks per page)", got)
	}

	view.PageDown()
	if got := selectedThread(view); got != "c1" {
		t.Fatalf("second PageDown selected %q, want c1", got)
	}

	view.PageUp()
	if got := selectedThread(view); got != "b2" {
		t.Fatalf("PageUp from c1 selected %q, want b2", got)
	}
}

// Mirrors Rust #63145: a single-group list pages by its full row count because
// no heading or gap consumes the viewport.
func TestAgentsOverviewPageDownSingleGroupUsesRowCountLikeRust(t *testing.T) {
	rows := []Row{
		{ThreadID: "one", CWD: "/work/a", Group: GroupWorking, UpdatedAt: 2},
		{ThreadID: "two", CWD: "/work/a", Group: GroupWorking, UpdatedAt: 1},
	}
	view := New(rows, "", false)
	view.pageHeight = 2
	view.Selected = 0
	view.PageDown()
	if got := selectedThread(view); got != "two" {
		t.Fatalf("PageDown selected %q, want two", got)
	}
	view.PageDown()
	if got := selectedThread(view); got != "two" {
		t.Fatalf("PageDown past the end selected %q, want to stay on two", got)
	}
}
