package tea

import (
	"strings"
	"testing"

	"codex_go/tui/mousemsg"
)

// Rust #61922 side_focus: a sidebar row is activated only when the release
// lands on the row the press captured. A drag that starts on one row and ends
// on another must start nothing, while the matching pair runs the row action.
func TestSidebarChooserRowActivationRequiresMatchingPressAndReleaseLikeRust(t *testing.T) {
	model, started := newSidebarTabsModel(t)
	openChooserWithFiles(t, model, nil, "")
	model.sidebarChoices = []SideChatChoice{{ThreadID: "saved-1"}, {ThreadID: "saved-2"}}
	model.transcript.Height = 24
	model.sidebarTabStripTop = 0
	rows := strings.Split(model.renderSidebarChooser(model.sidebarTabStripWidth(), 24), "\n")
	rowForChoice := func(want int) int {
		t.Helper()
		for row := 0; row < len(rows); row++ {
			if index, ok := model.sidebarChooserChoiceAtRow(row); ok && index == want {
				return row
			}
		}
		t.Fatalf("no chooser row maps to choice %d", want)
		return -1
	}
	left, _, _, ok := model.sidePaneLayout(model.width)
	if !ok {
		t.Fatal("side pane layout unavailable")
	}
	x := left + sideWorkspaceDividerWidth + 2
	newChatRow := rowForChoice(0)
	savedRow := rowForChoice(1)

	if handled, _ := model.handleSidePaneMouse(mousemsg.MouseMsg{
		Action: mousemsg.MouseActionPress, Button: mousemsg.MouseButtonLeft, X: x, Y: savedRow,
	}); !handled {
		t.Fatal("press on a saved chat row was not handled")
	}
	// The release lands on the new-chat row, so a handler that activated the
	// release target would start a chat here.
	if _, cmd := model.handleSidePaneMouse(mousemsg.MouseMsg{
		Action: mousemsg.MouseActionRelease, Button: mousemsg.MouseButtonLeft, X: x, Y: newChatRow,
	}); cmd != nil {
		runTeaCmd(t, model, cmd)
	}
	if len(*started) != 0 {
		t.Fatalf("a press on one row released on another started %d side chats, want none", len(*started))
	}
	if !model.sidebarChooserVisible() {
		t.Fatal("a rejected drag closed the chooser")
	}

	if handled, _ := model.handleSidePaneMouse(mousemsg.MouseMsg{
		Action: mousemsg.MouseActionPress, Button: mousemsg.MouseButtonLeft, X: x, Y: newChatRow,
	}); !handled {
		t.Fatal("press on the new-chat row was not handled")
	}
	_, cmd := model.handleSidePaneMouse(mousemsg.MouseMsg{
		Action: mousemsg.MouseActionRelease, Button: mousemsg.MouseButtonLeft, X: x, Y: newChatRow,
	})
	runTeaCmd(t, model, cmd)
	if len(*started) != 1 {
		t.Fatalf("side starters = %d, want 1 after a matching press and release", len(*started))
	}
	if model.sidebarChooserVisible() {
		t.Fatal("a matching release left the chooser open")
	}
}
