package tea

import (
	"testing"
)

// Rust #62157: leaving the watch-only reader returns to the subagent list with
// the reader's agent still selected, and the parent conversation stays active.
func TestSidebarSubagentReaderReturnsToListLikeRust(t *testing.T) {
	model, _ := newSidebarSubagentPaneModel(t)
	runTeaCmd(t, model, model.switchToSidebarAgent("agent-1"))
	if model.activeSide == nil || !model.activeSide.WatchOnly {
		t.Fatalf("active side = %#v, want the watch-only reader", model.activeSide)
	}

	runTeaCmd(t, model, model.focusSidebarContent())

	if !model.sidebarChooserVisible() || !model.sidebarChooserFocused {
		t.Fatalf("chooser = open %t focused %t, want the subagent list", model.sidebarChooserOpen, model.sidebarChooserFocused)
	}
	row, ok := model.sidebarChooserSelectedAgent()
	if !ok || row.ThreadID != "agent-1" {
		t.Fatalf("selected row = %#v ok=%t, want the reader's agent", row, ok)
	}
	if model.State.ThreadID != "parent-a" {
		t.Fatalf("main thread = %q, want the parent conversation", model.State.ThreadID)
	}
	if model.activeSide == nil || !model.activeSide.WatchOnly || model.activeSide.SideThreadID != "agent-1" {
		t.Fatalf("reader changed: %#v", model.activeSide)
	}
}

// Picking another Subagents row replaces the reader's transcript instead of
// stacking a second tab for the same parent conversation.
func TestSidebarSubagentReaderReplacesDetailLikeRust(t *testing.T) {
	model, closed := newSidebarSubagentPaneModel(t)
	runTeaCmd(t, model, model.switchToSidebarAgent("agent-1"))

	runTeaCmd(t, model, model.switchToSidebarAgent("agent-2"))

	side := model.activeSide
	if side == nil || !side.WatchOnly || side.SideThreadID != "agent-2" {
		t.Fatalf("active side = %#v, want the replacement reader", side)
	}
	if tabs := model.sidebarTabsFor("parent-a"); len(tabs) != 1 || tabs[0].SideThreadID != "agent-2" {
		t.Fatalf("tabs = %#v, want one reader tab", tabs)
	}
	if *closed != 0 {
		t.Fatalf("close callback ran %d times while replacing the reader", *closed)
	}
	if model.State.ThreadID != "parent-a" {
		t.Fatalf("main thread = %q, want the parent conversation", model.State.ThreadID)
	}
}
