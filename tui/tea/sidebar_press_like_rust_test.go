package tea

import (
	"testing"

	bubbletea "charm.land/bubbletea/v2"
	mousemsg "codex_go/tui/mousemsg"
)

// clickSidebarPane dispatches the press and release one terminal click
// delivers and drains both halves. Rust #61922 side_focus.rs activates a
// sidebar control only when the release lands on the target the press
// captured, so tests must send both events.
func clickSidebarPane(t *testing.T, model *Model, button mousemsg.MouseButton, x int, y int) (bool, bubbletea.Cmd) {
	t.Helper()
	handled, cmd := model.handleSidePaneMouse(mousemsg.MouseMsg{
		Action: mousemsg.MouseActionPress, Button: button, X: x, Y: y,
	})
	if !handled {
		return false, nil
	}
	runTeaCmd(t, model, cmd)
	handled, cmd = model.handleSidePaneMouse(mousemsg.MouseMsg{
		Action: mousemsg.MouseActionRelease, Button: button, X: x, Y: y,
	})
	return handled, cmd
}

// Rust #61922 side_focus.rs keeps the target a pointer pressed in
// App::sidebar_press and only dispatches the action when the release reports
// the same target and pane state. A drag that starts on one tab and ends on
// another must not switch either tab; a matching pair must switch.
func TestSidebarTabActivationRequiresMatchingPressAndReleaseLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	first := &activeSideConversation{ParentThreadID: "parent-a", SideThreadID: "side-1"}
	second := &activeSideConversation{ParentThreadID: "parent-a", SideThreadID: "side-2"}
	model.registerSidebarTab(first)
	model.registerSidebarTab(second)
	if cmd := model.activateRetainedSideConversation(first, ""); cmd != nil {
		runTeaCmd(t, model, cmd)
	}
	_ = model.viewString()

	left, right, _, ok := model.sidePaneLayout(model.width)
	if !ok || right <= 0 {
		t.Fatalf("split layout unavailable: right=%d ok=%t", right, ok)
	}
	base := left + sideWorkspaceDividerWidth
	tabWidth, count := sidebarTabLayout(sidebarTabLabels(2, false), right)
	if count < 2 || tabWidth <= 0 {
		t.Fatalf("strip fits %d tabs of %d columns, want 2", count, tabWidth)
	}
	firstColumn := base + 1
	secondColumn := base + tabWidth + 1

	// Pressing the second tab keeps the press captured but changes nothing.
	handled, cmd := model.handleSidePaneMouse(mousemsg.MouseMsg{
		Action: mousemsg.MouseActionPress, Button: mousemsg.MouseButtonLeft,
		X: secondColumn, Y: model.sidebarTabStripTop,
	})
	if !handled {
		t.Fatal("press on the second tab was not handled")
	}
	runTeaCmd(t, model, cmd)
	if model.activeSide == nil || model.activeSide.SideThreadID != "side-1" {
		t.Fatalf("press alone selected %#v, want side-1 still active", model.activeSide)
	}

	// Dragging onto the first tab and releasing there must not activate it.
	// Motion only repaints the hover, so it reports handled only when the
	// highlighted cell changed; the press capture is what the release compares.
	_, motionCmd := model.handleSidePaneMouse(mousemsg.MouseMsg{
		Action: mousemsg.MouseActionMotion, X: firstColumn, Y: model.sidebarTabStripTop,
	})
	runTeaCmd(t, model, motionCmd)
	handled, cmd = model.handleSidePaneMouse(mousemsg.MouseMsg{
		Action: mousemsg.MouseActionRelease, Button: mousemsg.MouseButtonLeft,
		X: firstColumn, Y: model.sidebarTabStripTop,
	})
	if !handled {
		t.Fatal("release after the drag was not handled")
	}
	runTeaCmd(t, model, cmd)
	if model.activeSide == nil || model.activeSide.SideThreadID != "side-1" {
		t.Fatalf("drag activated %#v, want side-1 untouched", model.activeSide)
	}
	if model.sidebarPress != nil {
		t.Fatal("release kept the captured press")
	}

	// A press and release on the same tab switches it.
	handled, cmd = clickSidebarPane(t, model, mousemsg.MouseButtonLeft, secondColumn, model.sidebarTabStripTop)
	if !handled {
		t.Fatal("matching press/release was not handled")
	}
	runTeaCmd(t, model, cmd)
	if model.State.ThreadID != "side-2" {
		t.Fatalf("matching click left thread %q, want side-2", model.State.ThreadID)
	}
	if model.sidebarPress != nil {
		t.Fatal("matching click kept the captured press")
	}
}

// Rust side_focus.rs hands the chooser's focus back to the main composer when
// the pointer presses outside the panel: the list stays open with its
// selection, but typing goes to the main composer again.
func TestSidebarChooserBackgroundPressReturnsFocusToMainLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	if cmd := model.focusSidebarContent(); cmd != nil {
		runTeaCmd(t, model, cmd)
	}
	if !model.sidebarChooserVisible() || !model.sidebarChooserFocused {
		t.Fatalf("chooser did not open focused: open=%t focused=%t", model.sidebarChooserOpen, model.sidebarChooserFocused)
	}
	choice := model.sidebarChoice

	handled, cmd := model.handleSidePaneMouse(mousemsg.MouseMsg{
		Action: mousemsg.MouseActionPress, Button: mousemsg.MouseButtonLeft, X: 2, Y: 3,
	})
	if !handled {
		t.Fatal("press in the main pane was not handled")
	}
	runTeaCmd(t, model, cmd)
	if model.sidebarChooserFocused {
		t.Fatal("press in the main pane left the chooser focused")
	}
	if !model.sidebarChooserVisible() {
		t.Fatal("press in the main pane closed the chooser")
	}
	if model.sidebarChoice != choice {
		t.Fatalf("press in the main pane moved the selection from %d to %d", choice, model.sidebarChoice)
	}
}
