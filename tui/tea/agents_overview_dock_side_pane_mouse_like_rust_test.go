package tea

import (
	"testing"

	bubbletea "charm.land/bubbletea/v2"

	"github.com/charmbracelet/x/ansi"

	mousemsg "codex_go/tui/mousemsg"
)

// Rust app.rs hit tests the side panes against workspace_chat_area, so with the
// Command Center docked the pointer still lands on the pane it is painted over.
func TestAgentsOverviewDockSidePaneMouseLikeRust(t *testing.T) {
	model, _ := newSidebarSubagentPaneModel(t)
	model.ownedScreen = true
	model.Update(bubbletea.WindowSizeMsg{Width: 160, Height: 30})
	runTeaCmd(t, model, model.switchToSidebarAgent("agent-1"))
	model.applyAgentsCommand()
	_ = model.viewString()

	originX, left, right, _, ok := model.sidePaneLayoutInWorkspace()
	if !ok || right <= 1 || left <= 1 {
		t.Fatalf("no side layout inside the dock: ok=%t left=%d right=%d", ok, left, right)
	}
	if want := agentsOverviewDockCenterWidth + agentsOverviewDockGutter; originX != want {
		t.Fatalf("conversation origin = %d, want %d", originX, want)
	}
	if model.sidebarReaderFocused {
		t.Fatal("side pane started focused")
	}

	// The painted side pane sits inside the conversation column, right of the
	// dock and the side divider.
	model.Update(mousemsg.MouseMsg{
		Action: mousemsg.MouseActionPress, Button: mousemsg.MouseButtonLeft,
		X: originX + left + ansi.StringWidth(sideWorkspaceDivider) + 2, Y: 3,
	})
	if !model.sidebarReaderFocused {
		t.Fatal("a press on the docked side pane did not focus it")
	}
}

// Dragging the side divider still resizes the side conversation when the
// Command Center owns the left column.
func TestAgentsOverviewDockSideDividerDragLikeRust(t *testing.T) {
	model, _ := newSidebarSubagentPaneModel(t)
	model.ownedScreen = true
	model.Update(bubbletea.WindowSizeMsg{Width: 160, Height: 30})
	runTeaCmd(t, model, model.switchToSidebarAgent("agent-1"))
	model.applyAgentsCommand()
	_ = model.viewString()

	originX, left, right, _, ok := model.sidePaneLayoutInWorkspace()
	if !ok || left <= 1 {
		t.Fatalf("no side layout inside the dock: ok=%t left=%d", ok, left)
	}
	dividerX := originX + left
	model.Update(mousemsg.MouseMsg{Action: mousemsg.MouseActionPress, Button: mousemsg.MouseButtonLeft, X: dividerX, Y: 2})
	if model.sideDrag == nil {
		t.Fatal("a press on the docked side divider did not start a resize")
	}
	model.Update(mousemsg.MouseMsg{Action: mousemsg.MouseActionMotion, Button: mousemsg.MouseButtonLeft, X: dividerX + 10, Y: 2})
	if model.sideResize == nil {
		t.Fatal("the drag painted no resize preview")
	}
	grown := model.sideResize.previewWidth
	model.Update(mousemsg.MouseMsg{Action: mousemsg.MouseActionRelease, Button: mousemsg.MouseButtonLeft, X: dividerX + 10, Y: 2})
	if model.sideDrag != nil || model.sideResize != nil {
		t.Fatal("the drag did not finish")
	}
	if grown >= right {
		t.Fatalf("drag right shrank the side pane: %d -> %d", right, grown)
	}
	_, areaWidth := model.workspaceChatArea()
	if model.sideExactWidth.total != areaWidth {
		t.Fatalf("saved width total = %d, want the conversation area %d", model.sideExactWidth.total, areaWidth)
	}
}
