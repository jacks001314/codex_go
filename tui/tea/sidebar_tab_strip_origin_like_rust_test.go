package tea

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	mousemsg "codex_go/tui/mousemsg"
)

// Rust #59928: sidebar_tab_strip.rs hit-tests the row the strip was painted on,
// not a fixed viewport row. Content above the sidebar (here one blank row from
// a conversation inset) must move the hit targets with the strip.
func TestSidebarTabStripMouseHitFollowsRenderedOriginLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	first := &activeSideConversation{ParentThreadID: "parent-a", SideThreadID: "side-1"}
	second := &activeSideConversation{ParentThreadID: "parent-a", SideThreadID: "side-2"}
	model.registerSidebarTab(first)
	model.registerSidebarTab(second)
	if cmd := model.activateRetainedSideConversation(first, ""); cmd != nil {
		runTeaCmd(t, model, cmd)
	}

	inset := ConversationRect{X: 0, Y: 1, Width: 120, Height: 24}
	model.conversationInset = &inset
	view := model.viewString()
	model.conversationInset = nil
	if model.sidebarTabStripTop != inset.Y {
		t.Fatalf("strip origin = %d, want the painted row %d", model.sidebarTabStripTop, inset.Y)
	}
	lines := strings.Split(view, "\n")
	if len(lines) <= inset.Y {
		t.Fatalf("view has %d rows, want the strip on row %d", len(lines), inset.Y)
	}
	if !strings.Contains(lines[inset.Y], " Side 1 ") {
		t.Fatalf("row %d does not carry the tab strip: %q", inset.Y, lines[inset.Y])
	}

	left, right, _, ok := model.sidePaneLayout(model.width)
	if !ok || right <= 0 {
		t.Fatalf("split layout unavailable: right=%d ok=%t", right, ok)
	}
	base := left + ansi.StringWidth(sideWorkspaceDivider)
	tabWidth, count := sidebarTabLayout(sidebarTabLabels(2), right)
	if count < 2 || tabWidth <= 0 {
		t.Fatalf("strip fits %d tabs of %d columns, want 2", count, tabWidth)
	}
	secondColumn := base + tabWidth + 1

	// The first viewport row is blank in this layout. A press there shares the
	// second tab's column but must not select it.
	if handled, _ := model.handleSidePaneMouse(mousemsg.MouseMsg{
		Action: mousemsg.MouseActionPress, Button: mousemsg.MouseButtonLeft, X: secondColumn, Y: 0,
	}); handled {
		t.Fatalf("press on row 0 was handled as a tab press; the strip is painted on row %d",
			model.sidebarTabStripTop)
	}
	if model.activeSide == nil || model.activeSide.SideThreadID != "side-1" {
		t.Fatalf("press on row 0 selected %#v, want side-1 untouched", model.activeSide)
	}

	handled, cmd := model.handleSidePaneMouse(mousemsg.MouseMsg{
		Action: mousemsg.MouseActionPress, Button: mousemsg.MouseButtonLeft,
		X: secondColumn, Y: model.sidebarTabStripTop,
	})
	if !handled {
		t.Fatalf("press on the painted strip row %d was not handled", model.sidebarTabStripTop)
	}
	runTeaCmd(t, model, cmd)
	if model.activeSide == nil || model.activeSide.SideThreadID != "side-2" {
		t.Fatalf("press on the painted strip row selected %#v, want side-2", model.activeSide)
	}
}
