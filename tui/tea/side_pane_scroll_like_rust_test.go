package tea

import (
	mousemsg "codex_go/tui/mousemsg"
	"github.com/charmbracelet/x/ansi"
	"strconv"
	"strings"
	"testing"

	"codex_go/protocol"
	codextui "codex_go/tui"
)

// Rust #59527: the wheel scrolls the pane under the pointer, including the
// inactive one, and the focused pane keeps its own position.
func TestSidePaneWheelScrollsUnfocusedPaneLikeRust(t *testing.T) {
	m := sideInteractionModel(t)
	m.activeSide.SideMessages = nil
	m.retainSideConversation(m.activeSide)
	for index := 0; index < 300; index++ {
		m.applyThreadScopedEvent(ThreadScopedEventMsg{
			ThreadID: "side",
			Event: protocol.ThreadEvent{
				Type:  "item.delta",
				Delta: &protocol.Delta{Text: "side line " + strconv.Itoa(index) + "\n"},
			},
		})
	}
	rendered, ok := m.renderSideWorkspace(140, 12)
	if !ok {
		t.Fatal("side workspace did not render")
	}
	if strings.Contains(rendered, "side line 0") {
		t.Fatalf("tail should not show the first line:\n%s", rendered)
	}

	left, right, _, ok := m.sidePaneLayout(140)
	if !ok {
		t.Fatal("side pane layout unavailable")
	}
	rightX := left + ansi.StringWidth(sideWorkspaceDivider) + 1
	focusedBefore := m.transcript.YOffset
	for attempt := 0; attempt < 400 && m.activeSide.SideScroll > 0; attempt++ {
		handled, _ := m.handleSidePaneMouse(mousemsg.MouseMsg{
			Action: mousemsg.MouseActionPress,
			Button: mousemsg.MouseButtonWheelUp,
			X:      rightX,
			Y:      2,
		})
		if !handled {
			t.Fatal("wheel over the unfocused pane was not handled")
		}
	}
	if !m.activeSide.SideInteractionSaved {
		t.Fatal("unfocused pane did not take a scroll position")
	}
	if m.transcript.YOffset != focusedBefore {
		t.Fatalf("focused pane scrolled to %d", m.transcript.YOffset)
	}
	scrolled, ok := m.renderSideWorkspace(140, 12)
	if !ok {
		t.Fatal("scrolled side workspace did not render")
	}
	if !strings.Contains(scrolled, "side line 0") {
		t.Fatalf("unfocused pane did not scroll back to earlier output:\n%s", scrolled)
	}
	if right <= 0 {
		t.Fatalf("side width = %d", right)
	}

	// The wheel over the focused parent pane still moves the transcript.
	handled, _ := m.handleSidePaneMouse(mousemsg.MouseMsg{
		Action: mousemsg.MouseActionPress,
		Button: mousemsg.MouseButtonWheelUp,
		X:      1,
		Y:      2,
	})
	if !handled {
		t.Fatal("wheel over the focused pane was not handled")
	}
	if m.transcript.YOffset >= focusedBefore {
		t.Fatalf("focused pane did not scroll up: %d, before %d", m.transcript.YOffset, focusedBefore)
	}
}

// Rust #59527: each pane keeps its scroll position when focus moves to the
// other pane and back.
func TestSidePaneScrollSurvivesFocusChangeLikeRust(t *testing.T) {
	m := sideInteractionModel(t)
	m.activeSide.SideMessages = nil
	m.retainSideConversation(m.activeSide)
	for index := 0; index < 300; index++ {
		m.applyThreadScopedEvent(ThreadScopedEventMsg{
			ThreadID: "side",
			Event: protocol.ThreadEvent{
				Type:  "item.delta",
				Delta: &protocol.Delta{Text: "side line " + strconv.Itoa(index) + "\n"},
			},
		})
	}
	m.transcript.YOffset = 5
	m.activeSide.ParentScroll = 5
	m.activeSide.ParentInteractionSaved = true
	m.activeSide.SideScroll = 12
	m.activeSide.SideInteractionSaved = true

	m.focusMainComposer() // no side focus yet; still parent focused
	if m.activeSide.ShowingSide {
		t.Fatal("focus changed unexpectedly")
	}
	m.focusSidebarContent()
	if !m.activeSide.ShowingSide {
		t.Fatal("side pane did not take focus")
	}
	if m.transcript.YOffset != 12 {
		t.Fatalf("side scroll = %d, want the parked 12", m.transcript.YOffset)
	}
	m.focusMainComposer()
	if m.transcript.YOffset != 5 {
		t.Fatalf("parent scroll = %d, want the parked 5", m.transcript.YOffset)
	}
	rendered, ok := m.renderSideWorkspace(140, 12)
	if !ok {
		t.Fatal("side workspace did not render")
	}
	if strings.Contains(rendered, "side line 299") {
		t.Fatalf("unfocused side pane returned to the tail:\n%s", rendered)
	}
}

// Rust #59527: an answer that streamed into the unfocused pane is current when
// focus returns and does not appear twice.
func TestSidePaneFocusReturnKeepsLiveAnswerWithoutDuplicatesLikeRust(t *testing.T) {
	m := sideInteractionModel(t)
	m.activeSide.SideMessages = nil
	m.retainSideConversation(m.activeSide)
	m.applyThreadScopedEvent(ThreadScopedEventMsg{
		ThreadID: "side",
		Event:    protocol.ThreadEvent{Type: "item.delta", Delta: &protocol.Delta{Text: "hello"}},
	})
	m.applyThreadScopedEvent(ThreadScopedEventMsg{
		ThreadID: "side",
		Event: protocol.ThreadEvent{Type: "item.completed", Item: &protocol.ThreadItem{
			Type: "agent_message",
			Text: "hello world",
		}},
	})
	rendered, ok := m.renderSideWorkspace(140, 12)
	if !ok {
		t.Fatal("side workspace did not render")
	}
	if got := strings.Count(rendered, "hello world"); got != 1 {
		t.Fatalf("unfocused pane rendered the answer %d times:\n%s", got, rendered)
	}
	m.focusSidebarContent()
	assistant := 0
	for _, message := range m.State.Messages {
		if message.Role == codextui.RoleAssistant && strings.Contains(message.Text, "hello world") {
			assistant++
		}
	}
	if assistant != 1 {
		t.Fatalf("focused pane has %d copies of the answer: %#v", assistant, m.State.Messages)
	}
	m.focusMainComposer()
	rendered, ok = m.renderSideWorkspace(140, 12)
	if !ok {
		t.Fatal("side workspace did not render after returning")
	}
	if got := strings.Count(rendered, "hello world"); got != 1 {
		t.Fatalf("returning focus duplicated the answer %d times:\n%s", got, rendered)
	}
}
