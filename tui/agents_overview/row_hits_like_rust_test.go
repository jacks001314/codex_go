package agentsoverview

import (
	"strings"
	"testing"
)

// Rust #62990 click_row: the render records which task each painted row shows,
// so a pointer click can resolve the row under it.
func TestRowHitsFollowPaintedRowsLikeRust(t *testing.T) {
	view := New([]Row{
		{ThreadID: "alpha", Name: "alpha", CWD: "work"},
		{ThreadID: "beta", Name: "beta", CWD: "work"},
	}, "alpha", false)
	lines := view.Render(100, 24)
	row := -1
	for index, line := range lines {
		if strings.Contains(line, "beta") {
			row = index
			break
		}
	}
	if row < 0 {
		t.Fatalf("beta row not painted:\n%s", strings.Join(lines, "\n"))
	}
	if id, ok := view.ThreadAtRow(row); !ok || id != "beta" {
		t.Fatalf("ThreadAtRow(%d) = %q ok=%t, want beta", row, id, ok)
	}
	if _, ok := view.ThreadAtRow(0); ok {
		t.Fatal("header row resolved to a task")
	}
	if !view.SelectThread("beta") || view.SelectedThreadID() != "beta" {
		t.Fatalf("SelectThread(beta) = %q", view.SelectedThreadID())
	}
	if view.SelectThread("missing") {
		t.Fatal("SelectThread accepted an unknown task")
	}
}

// The painted "Show more" row resolves for a click too.
func TestShowMoreRowHitLikeRust(t *testing.T) {
	view := New([]Row{{ThreadID: "alpha", Name: "alpha", CWD: "work"}}, "alpha", false)
	view.SetHasMore(true)
	lines := view.Render(100, 24)
	row := -1
	for index, line := range lines {
		if strings.Contains(line, view.ShowMoreLabel()) {
			row = index
			break
		}
	}
	if row < 0 {
		t.Fatalf("show more row not painted:\n%s", strings.Join(lines, "\n"))
	}
	if !view.ShowMoreAtRow(row) {
		t.Fatalf("ShowMoreAtRow(%d) = false", row)
	}
	if !view.SelectShowMore() || !view.ShowMoreSelected() {
		t.Fatal("SelectShowMore did not move the selection")
	}
}
