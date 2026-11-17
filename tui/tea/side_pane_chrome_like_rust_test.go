package tea

import (
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	codextui "codex_go/tui"
	mousemsg "codex_go/tui/mousemsg"
)

// Rust side_pane.rs lays the split out as [main][GAP][sidebar] with GAP = 1 and
// paints the divider glyph inside that single column. The snapshot in
// codex-rs/tui/src/snapshots/...single_side_chat.snap shows the side pane
// starting on the column right after the divider.
func TestSideWorkspaceDividerUsesRustGapColumnLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 24})
	model.featureSettings = map[string]bool{sideWorkspaceFeatureKey: true}
	model.State.ThreadID = "root"
	model.activeSide = &activeSideConversation{
		ParentThreadID: "root",
		SideThreadID:   "side",
		ParentMessages: []codextui.Message{{Role: codextui.RoleUser, Text: "parent line"}},
		SideMessages:   []codextui.Message{{Role: codextui.RoleAssistant, Text: "side line"}},
		ShowingSide:    true,
	}
	model.refreshTranscript()

	left, right, _, ok := model.sidePaneLayout(model.width)
	if !ok {
		t.Fatal("split layout unavailable")
	}
	if left+sideWorkspaceDividerWidth+right != model.width {
		t.Fatalf("panes %d+%d+%d do not fill %d columns", left, sideWorkspaceDividerWidth, right, model.width)
	}

	view := model.viewString()
	for index, row := range strings.Split(view, "\n") {
		if width := ansi.StringWidth(row); width != model.width {
			t.Fatalf("row %d width = %d, want %d: %q", index, width, model.width, ansi.Strip(row))
		}
		if got := ansi.Strip(ansi.Cut(row, left, left+sideWorkspaceDividerWidth)); got != sideWorkspaceDivider {
			t.Fatalf("row %d divider = %q, want the single %q column: %q",
				index, got, sideWorkspaceDivider, ansi.Strip(row))
		}
	}
	// No padding column separates the divider from the sidebar pane.
	first := strings.Split(view, "\n")[0]
	if got := ansi.Strip(ansi.Cut(first, left+sideWorkspaceDividerWidth, left+sideWorkspaceDividerWidth+1)); got != " " {
		t.Fatalf("sidebar pane does not start right after the divider: %q", ansi.Strip(first))
	}
}

// Rust sidebar_tab_strip.rs reserves both gutter columns when the tabs
// overflow and paints ‹ / › there; a press on an arrow activates the hidden
// neighbour (TabTarget::Overflow).
func TestSidebarTabStripOverflowArrowsLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	tabs := make([]*activeSideConversation, 0, 8)
	for index := 1; index <= 8; index++ {
		tabs = append(tabs, &activeSideConversation{
			ParentThreadID: "parent-a",
			SideThreadID:   "side-" + strconv.Itoa(index),
		})
	}
	model.sidebarTabs = map[string][]*activeSideConversation{"parent-a": tabs}
	model.activeSide = tabs[6]
	model.activeSide.ShowingSide = true
	model.refreshTranscript()
	_ = model.viewString()

	width := model.sidebarTabStripWidth()
	window := model.sidebarTabStripWindowWidth(width)
	if window.gutter != sidebarTabOverflowGutter || !window.leftArrow || !window.rightArrow {
		t.Fatalf("overflow window gutter = %d left=%t right=%t", window.gutter, window.leftArrow, window.rightArrow)
	}
	strip := ansi.Strip(model.renderSidebarTabStrip(width))
	if got := ansi.Cut(strip, 0, 1); got != sidebarTabOverflowLeft {
		t.Fatalf("left gutter = %q, want %q: %q", got, sidebarTabOverflowLeft, strip)
	}
	if got := ansi.Cut(strip, window.addStart-1, window.addStart); got != sidebarTabOverflowRight {
		t.Fatalf("right gutter = %q, want %q: %q", got, sidebarTabOverflowRight, strip)
	}
	// The reserved gutter keeps the tab cells one column off the pane edge.
	if got := ansi.Cut(strip, 1, window.gutter); got != strings.Repeat(" ", window.gutter-1) {
		t.Fatalf("second gutter column = %q, want blank: %q", got, strip)
	}

	left, _, _, ok := model.sidePaneLayout(model.width)
	if !ok {
		t.Fatal("split layout unavailable")
	}
	base := left + sideWorkspaceDividerWidth
	handled, cmd := model.handleSidePaneMouse(mousemsg.MouseMsg{
		Action: mousemsg.MouseActionPress, Button: mousemsg.MouseButtonLeft,
		X: base, Y: model.sidebarTabStripTop,
	})
	if !handled {
		t.Fatal("press on the left arrow was not handled")
	}
	runTeaCmd(t, model, cmd)
	if model.activeSide != tabs[window.first-1] {
		t.Fatalf("left arrow activated %#v, want %#v", model.activeSide, tabs[window.first-1])
	}

	_ = model.viewString()
	window = model.sidebarTabStripWindowWidth(width)
	handled, cmd = model.handleSidePaneMouse(mousemsg.MouseMsg{
		Action: mousemsg.MouseActionPress, Button: mousemsg.MouseButtonLeft,
		X: base + window.addStart - 1, Y: model.sidebarTabStripTop,
	})
	if !handled {
		t.Fatal("press on the right arrow was not handled")
	}
	runTeaCmd(t, model, cmd)
	if model.activeSide != tabs[window.first+window.count] {
		t.Fatalf("right arrow activated %#v, want %#v", model.activeSide, tabs[window.first+window.count])
	}
}

// Rust preferred_width takes the saved sidebar percentage of the whole
// terminal and clamps it (side_pane.rs); codex-rs/tui/src/app/tests.rs pins 30%
// of 200 columns to a 139-column parent and 50% to 99.
func TestSideWorkspaceWidthsUseRustPercentMathLikeRust(t *testing.T) {
	left, right, ok := sideWorkspaceWidthsForPercent(200, 30, sideExactWidth{})
	if !ok || left != 139 || right != 60 {
		t.Fatalf("30%% of 200 = %d/%d ok=%t, want 139/60", left, right, ok)
	}
	left, right, ok = sideWorkspaceWidthsForPercent(200, 50, sideExactWidth{})
	if !ok || left != 99 || right != 100 {
		t.Fatalf("50%% of 200 = %d/%d ok=%t, want 99/100", left, right, ok)
	}
	if _, _, ok := sideWorkspaceWidthsForPercent(sideWorkspaceMinWidth-1, 30, sideExactWidth{}); ok {
		t.Fatalf("split enabled below %d columns", sideWorkspaceMinWidth)
	}
}

// Rust paints the close glyph in the last column of every tab cell
// (sidebar_chooser::close_cell) and closes that tab when the cell is clicked.
func TestSidebarTabStripCloseCellClosesTabLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	tabs := []*activeSideConversation{
		{ParentThreadID: "parent-a", SideThreadID: "side-a"},
		{ParentThreadID: "parent-a", SideThreadID: "side-b"},
	}
	model.sidebarTabs = map[string][]*activeSideConversation{"parent-a": tabs}
	model.activeSide = tabs[1]
	model.activeSide.ShowingSide = true
	model.refreshTranscript()
	_ = model.viewString()

	width := model.sidebarTabStripWidth()
	window := model.sidebarTabStripWindowWidth(width)
	if window.count != len(tabs) {
		t.Fatalf("strip paints %d tabs, want %d", window.count, len(tabs))
	}
	strip := ansi.Strip(model.renderSidebarTabStrip(width))
	for offset := 0; offset < window.count; offset++ {
		start := offset * window.stride
		if got := ansi.Cut(strip, start+window.tabWidth-1, start+window.tabWidth); got != sidebarTabCloseGlyph {
			t.Fatalf("tab %d close cell = %q, want %q: %q", offset, got, sidebarTabCloseGlyph, strip)
		}
	}

	left, _, _, ok := model.sidePaneLayout(model.width)
	if !ok {
		t.Fatal("split layout unavailable")
	}
	closeColumn := left + sideWorkspaceDividerWidth + window.tabWidth - 1
	handled, cmd := model.handleSidePaneMouse(mousemsg.MouseMsg{
		Action: mousemsg.MouseActionPress, Button: mousemsg.MouseButtonLeft,
		X: closeColumn, Y: model.sidebarTabStripTop,
	})
	if !handled {
		t.Fatalf("press on the close cell at column %d was not handled", closeColumn)
	}
	runTeaCmd(t, model, cmd)
	remaining := model.sidebarTabsFor("parent-a")
	if len(remaining) != 1 || remaining[0].SideThreadID != "side-b" {
		t.Fatalf("tabs after close = %#v, want only side-b", remaining)
	}
	if model.notice != "Closed side chat." {
		t.Fatalf("notice = %q, want the closed-tab notice", model.notice)
	}

	// The cell next to the close glyph still selects the tab instead of closing.
	resetTabs := func() []*activeSideConversation {
		fresh := []*activeSideConversation{
			{ParentThreadID: "parent-a", SideThreadID: "side-a"},
			{ParentThreadID: "parent-a", SideThreadID: "side-b"},
		}
		model.sidebarTabs = map[string][]*activeSideConversation{"parent-a": fresh}
		model.activeSide = fresh[1]
		model.activeSide.ShowingSide = true
		model.refreshTranscript()
		return fresh
	}
	resetTabs()
	handled, cmd = model.handleSidePaneMouse(mousemsg.MouseMsg{
		Action: mousemsg.MouseActionPress, Button: mousemsg.MouseButtonLeft,
		X: left + sideWorkspaceDividerWidth + window.tabWidth - 2, Y: model.sidebarTabStripTop,
	})
	if !handled {
		t.Fatal("press on the tab title was not handled as a selection")
	}
	runTeaCmd(t, model, cmd)
	if tabs := model.sidebarTabsFor("parent-a"); len(tabs) != 2 {
		t.Fatalf("title press closed a tab: %#v", tabs)
	}

	// Rust also closes the tab on a middle click anywhere in its cell.
	resetTabs()
	_ = model.viewString()
	handled, cmd = model.handleSidePaneMouse(mousemsg.MouseMsg{
		Action: mousemsg.MouseActionPress, Button: mousemsg.MouseButtonMiddle,
		X: left + sideWorkspaceDividerWidth + 2, Y: model.sidebarTabStripTop,
	})
	if !handled {
		t.Fatal("middle press on the tab was not handled as a close")
	}
	runTeaCmd(t, model, cmd)
	if tabs := model.sidebarTabsFor("parent-a"); len(tabs) != 1 || tabs[0].SideThreadID != "side-b" {
		t.Fatalf("tabs after middle click = %#v, want only side-b", tabs)
	}
}

// Rust side_pane::paint_divider dims the one-column gap until the pointer
// hovers it (app/link_hover.rs maps the divider to PointerShape::Resize) or a
// resize/drag owns the column, and then paints it with the accent colour.
func TestSideDividerAccentsOnHoverLikeRust(t *testing.T) {
	model := &Model{}
	if got := model.sideDividerCell(); got != "\x1b[2m"+sideWorkspaceDivider+"\x1b[22m" {
		t.Fatalf("idle divider = %q", got)
	}
	accent := transcriptDisclosureAccentSGR() + sideWorkspaceDivider + "\x1b[0m"
	model.sideDividerHover = true
	if got := model.sideDividerCell(); got != accent {
		t.Fatalf("hover divider = %q, want %q", got, accent)
	}
	model.sideDividerHover = false
	model.sideResize = &sidePaneResize{previewWidth: 40}
	if got := model.sideDividerCell(); got != accent {
		t.Fatalf("resize divider = %q, want %q", got, accent)
	}
	model.sideResize = nil
	model.sideDrag = &sidePaneDrag{}
	if got := model.sideDividerCell(); got != accent {
		t.Fatalf("drag divider = %q, want %q", got, accent)
	}

	// The pointer's motion events drive the hover: entering the gap column
	// requests a repaint, leaving it clears the accent again.
	view := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 24})
	view.featureSettings = map[string]bool{sideWorkspaceFeatureKey: true}
	view.State.SetThreadID("root")
	view.State.AddMessage(codextui.RoleUser, "hello from the main chat")
	view.activeSide = &activeSideConversation{
		ParentThreadID: "root",
		SideThreadID:   "side",
		ShowingSide:    true,
	}
	view.refreshTranscript()
	left, _, _, ok := view.sidePaneLayout(view.width)
	if !ok {
		t.Fatal("split layout unavailable")
	}
	_ = view.viewString()
	handled, _ := view.handleSidePaneMouse(mousemsg.MouseMsg{Action: mousemsg.MouseActionMotion, X: left, Y: 1})
	if !handled || !view.sideDividerHover {
		t.Fatalf("motion over the divider did not start the hover: handled=%t hover=%t", handled, view.sideDividerHover)
	}
	handled, _ = view.handleSidePaneMouse(mousemsg.MouseMsg{Action: mousemsg.MouseActionMotion, X: left + sideWorkspaceDividerWidth, Y: 1})
	if !handled || view.sideDividerHover {
		t.Fatalf("motion beside the divider did not clear the hover: handled=%t hover=%t", handled, view.sideDividerHover)
	}
}
