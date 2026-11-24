package tea

import (
	"testing"
)

// Rust #62157 side_focus: focus_sidebar_content selects the docked agent, so an
// open watch-only reader keeps its transcript and gains keyboard focus. Escape
// is the reader's way back to the Subagents list, and the parent conversation
// stays active throughout.
func TestSidebarSubagentReaderKeepsFocusLikeRust(t *testing.T) {
	model, _ := newSidebarSubagentPaneModel(t)
	runTeaCmd(t, model, model.switchToSidebarAgent("agent-1"))
	if model.activeSide == nil || !model.activeSide.WatchOnly {
		t.Fatalf("active side = %#v, want the watch-only reader", model.activeSide)
	}

	runTeaCmd(t, model, model.focusSidebarContent())

	if model.sidebarChooserVisible() {
		t.Fatal("focusing the sidebar replaced the reader with the subagent list")
	}
	if !model.sidebarReaderFocused {
		t.Fatal("focusing the sidebar left the reader without keyboard focus")
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
