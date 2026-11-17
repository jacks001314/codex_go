package tea

import (
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	codextui "codex_go/tui"
	mousemsg "codex_go/tui/mousemsg"
)

// Rust paints the parked conversation's return-to-latest affordance in the row
// between its transcript and its composer preview
// (transcript_view::follow_control + side_pane::paint_companion). The control
// is hidden while the parked view shows its tail, appears once the reader
// scrolls that pane up, gains the "New activity" prefix when output arrived
// meanwhile, and disappears again at the tail.
func TestSidePaneParkedFollowControlLikeRust(t *testing.T) {
	parent := make([]codextui.Message, 0, 40)
	for index := 0; index < 40; index++ {
		parent = append(parent, codextui.Message{
			Role: codextui.RoleUser,
			Text: "parent-" + strconv.Itoa(index),
		})
	}
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 24})
	model.featureSettings = map[string]bool{sideWorkspaceFeatureKey: true}
	model.State.ThreadID = "root"
	model.State.Messages = []codextui.Message{{Role: codextui.RoleUser, Text: "side question"}}
	model.activeSide = &activeSideConversation{
		ParentThreadID: "root",
		SideThreadID:   "side",
		ParentMessages: parent,
		SideMessages:   []codextui.Message{{Role: codextui.RoleUser, Text: "side question"}},
		ShowingSide:    true,
	}
	model.sidebarTabs = map[string][]*activeSideConversation{"root": {model.activeSide}}
	model.refreshTranscript()

	left, _, _, ok := model.sidePaneLayout(model.width)
	if !ok || left <= 0 {
		t.Fatalf("split layout unavailable: left=%d ok=%t", left, ok)
	}
	parentColumn := func() string {
		t.Helper()
		rows := strings.Split(model.viewString(), "\n")
		column := make([]string, 0, len(rows))
		for _, row := range rows {
			column = append(column, ansi.Cut(row, 0, left))
		}
		return strings.Join(column, "\n")
	}

	if column := parentColumn(); strings.Contains(column, "Back to bottom") {
		t.Fatalf("parked tail painted a follow control:\n%s", column)
	}

	// Wheel up over the parent column parks it above its tail.
	handled, _ := model.handleSidePaneMouse(mousemsg.MouseMsg{
		Action: mousemsg.MouseActionPress, Button: mousemsg.MouseButtonWheelUp, X: 2, Y: 3,
	})
	if !handled {
		t.Fatal("wheel over the parent pane was not handled")
	}
	column := parentColumn()
	if !strings.Contains(column, "\u2193 Back to bottom \u00b7 esc") {
		t.Fatalf("scrolled parent pane is missing the follow control:\n%s", column)
	}
	if strings.Contains(column, "New activity") {
		t.Fatalf("follow control claimed new activity without new output:\n%s", column)
	}

	// Output that arrives while the pane stays parked raises the activity flag.
	model.activeSide.ParentMessages = append(model.activeSide.ParentMessages, codextui.Message{
		Role: codextui.RoleUser,
		Text: "parent-late",
	})
	column = parentColumn()
	if !strings.Contains(column, "New activity \u00b7 \u2193 Back to bottom \u00b7 esc") {
		t.Fatalf("parked pane misses the new-activity follow control:\n%s", column)
	}

	// Returning the pane to its tail clears both the control and the flag.
	model.activeSide.ParentScroll = model.activeSide.ParentViewMax
	model.activeSide.ParentInteractionSaved = true
	column = parentColumn()
	if strings.Contains(column, "Back to bottom") {
		t.Fatalf("follow control survived a jump to the tail:\n%s", column)
	}
	if model.activeSide.ParentUnseen {
		t.Fatal("parked pane kept unseen activity after painting its tail")
	}
}
