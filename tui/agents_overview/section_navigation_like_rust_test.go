package agentsoverview

import (
	"fmt"
	"testing"
)

// Rust agent_center::navigation::select_section moves the selection to the
// first task of the next or previous rendered group and clamps at either end.
func TestSelectSectionJumpsToGroupStartsLikeRust(t *testing.T) {
	view := New([]Row{
		{ThreadID: "a1", Group: GroupWorking},
		{ThreadID: "a2", Group: GroupWorking},
		{ThreadID: "b1", Group: GroupReady},
		{ThreadID: "b2", Group: GroupReady},
	}, "", false)
	view.State.Grouping = GroupingStatus
	view.Selected = 0

	view.SelectSection(true)
	if got := view.Rows[view.Selected].ThreadID; got != "b1" {
		t.Fatalf("next group selected %q, want the first task of the next group", got)
	}
	view.SelectSection(true)
	if got := view.Rows[view.Selected].ThreadID; got != "b1" {
		t.Fatalf("next group at the end selected %q, want to clamp", got)
	}
	view.SelectSection(false)
	if got := view.Rows[view.Selected].ThreadID; got != "a1" {
		t.Fatalf("previous group selected %q, want the first task of the previous group", got)
	}
	view.SelectSection(false)
	if got := view.Rows[view.Selected].ThreadID; got != "a1" {
		t.Fatalf("previous group at the top selected %q, want to clamp", got)
	}
}

// Rust centers "Show more" after the last group, so moving up from it lands on
// that group's first task and moving down does nothing.
func TestSelectSectionTreatsShowMoreAsAfterTheLastGroupLikeRust(t *testing.T) {
	rows := make([]Row, 0, 15)
	for i := 0; i < 15; i++ {
		rows = append(rows, Row{ThreadID: fmt.Sprintf("task-%02d", i), Group: GroupReady})
	}
	view := New(rows, "", false)
	view.State.Grouping = GroupingStatus
	view.SetHistoryPaging(true)
	view.Selected = ShowMoreIndex
	if !view.ShowMoreSelected() {
		t.Fatal("expected the Show more row to be selectable")
	}

	view.SelectSection(true)
	if !view.ShowMoreSelected() {
		t.Fatal("moving down from Show more should clamp")
	}
	view.SelectSection(false)
	if view.ShowMoreSelected() {
		t.Fatal("moving up from Show more should land on the last group")
	}
	if got := view.Selected; got != 0 {
		t.Fatalf("selected = %d, want the last group's first task", got)
	}
}
