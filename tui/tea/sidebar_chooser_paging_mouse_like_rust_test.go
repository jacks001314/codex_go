package tea

import (
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"codex_go/tui/keymsg"
	mousemsg "codex_go/tui/mousemsg"
)

// paintedChooserRows counts the choice rows a rendered chooser panel shows.
func paintedChooserRows(panel string) int {
	rows := 0
	for _, line := range strings.Split(panel, "\n") {
		title := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "›"))
		if strings.HasPrefix(title, "Side chat") || strings.HasPrefix(title, sidebarNewChatTitle) {
			rows++
		}
	}
	return rows
}

// Rust #59928: page_up/page_down move the chooser by one painted window of
// rows instead of a fixed constant, so a tall pane pages further than a short
// one and the selection lands on the last row the window shows.
func TestSidebarChooserPagesByVisibleRowsLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	for index := 1; index <= 60; index++ {
		model.registerSidebarTab(&activeSideConversation{
			ParentThreadID: "parent-a",
			SideThreadID:   "side-" + strconv.Itoa(index),
		})
	}
	if cmd := model.focusSidebarContent(); cmd != nil {
		runTeaCmd(t, model, cmd)
	}
	if !model.sidebarChooserVisible() {
		t.Fatal("chooser did not open")
	}
	_, right, _, ok := model.sidePaneLayout(model.width)
	if !ok || right <= 0 {
		t.Fatalf("split layout unavailable: right=%d ok=%t", right, ok)
	}

	window := map[int]int{}
	for _, height := range []int{20, 50} {
		model.transcript.Height = height
		model.sidebarChoice = 0
		visible := paintedChooserRows(model.renderSidebarChooser(right, height))
		if visible <= 0 {
			t.Fatalf("panel height %d painted no choice rows", height)
		}
		if visible == 5 {
			t.Fatalf("panel height %d painted the old fixed page size of 5 rows", height)
		}
		if handled, _ := model.updateSidebarChooserKey(key(keymsg.KeyCtrlF)); !handled {
			t.Fatalf("page_down was not handled at panel height %d", height)
		}
		if model.sidebarChoice != visible {
			t.Fatalf("page_down at panel height %d moved to row %d, want one window of %d rows",
				height, model.sidebarChoice, visible)
		}
		// The selection stays inside the painted window: it is the last row the
		// panel shows, exactly like Rust choice_rows keeps the choice visible.
		if panel := model.renderSidebarChooser(right, height); paintedChooserRows(panel) != visible {
			t.Fatalf("panel height %d painted the selection outside its window", height)
		}
		if handled, _ := model.updateSidebarChooserKey(key(keymsg.KeyCtrlB)); !handled || model.sidebarChoice != 0 {
			t.Fatalf("page_up at panel height %d handled=%t row=%d, want row 0", height, handled, model.sidebarChoice)
		}
		window[height] = visible
	}
	if window[20] == window[50] {
		t.Fatalf("both panel heights paged %d rows; the step is not driven by the visible window", window[20])
	}
}

// Rust #59928: the tab strip hit-tests the cells it paints - motion only moves
// the hover highlight, a left press selects that tab, and a press on " + "
// opens the chooser on the new-chat row without switching tabs.
func TestSidebarTabStripMouseSelectsVisibleTabLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	first := &activeSideConversation{ParentThreadID: "parent-a", SideThreadID: "side-1"}
	second := &activeSideConversation{ParentThreadID: "parent-a", SideThreadID: "side-2"}
	model.registerSidebarTab(first)
	model.registerSidebarTab(second)
	if cmd := model.activateRetainedSideConversation(first, ""); cmd != nil {
		runTeaCmd(t, model, cmd)
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

	handled, _ := model.handleSidePaneMouse(mousemsg.MouseMsg{
		Action: mousemsg.MouseActionMotion, X: secondColumn, Y: 0,
	})
	if !handled || model.sidebarTabHover != 1 {
		t.Fatalf("motion handled=%t hover=%d, want the second tab", handled, model.sidebarTabHover)
	}
	if model.State.ThreadID != "side-1" || model.activeSide != first {
		t.Fatalf("motion changed the selection: thread=%q side=%#v", model.State.ThreadID, model.activeSide)
	}

	handled, cmd := model.handleSidePaneMouse(mousemsg.MouseMsg{
		Action: mousemsg.MouseActionPress, Button: mousemsg.MouseButtonLeft, X: secondColumn, Y: 0,
	})
	if !handled {
		t.Fatal("press on the second tab was not handled")
	}
	runTeaCmd(t, model, cmd)
	if model.activeSide == nil || model.activeSide.SideThreadID != "side-2" {
		t.Fatalf("press on the second tab selected %#v", model.activeSide)
	}
	if model.State.ThreadID != "side-2" {
		t.Fatalf("press on the second tab left thread %q, want side-2", model.State.ThreadID)
	}

	handled, cmd = model.handleSidePaneMouse(mousemsg.MouseMsg{
		Action: mousemsg.MouseActionPress, Button: mousemsg.MouseButtonLeft, X: base + right - 2, Y: 0,
	})
	if !handled {
		t.Fatal("press on the + button was not handled")
	}
	runTeaCmd(t, model, cmd)
	if !model.sidebarChooserVisible() {
		t.Fatal("press on the + button did not open the chooser")
	}
	// Rust sidebar.rs show(Chooser): the choice starts on the selected tab
	// (falling back to the new-chat row), and the "+" row is offered below it.
	if want := 1; model.sidebarChoice != want {
		t.Fatalf("chooser row = %d, want the selected tab %d", model.sidebarChoice, want)
	}
	_, right, _, ok = model.sidePaneLayout(model.width)
	if panel := model.renderSidebarChooser(right, model.transcript.Height); !strings.Contains(panel, sidebarNewChatTitle) {
		t.Fatalf("chooser from + does not offer a new side chat:\n%s", panel)
	}
	if model.activeSide == nil || model.activeSide.SideThreadID != "side-2" {
		t.Fatalf("press on the + button switched tabs: %#v", model.activeSide)
	}
}
