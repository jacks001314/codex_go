package tea

import (
	"strconv"
	"strings"
	"testing"

	bubbletea "charm.land/bubbletea/v2"

	codextui "codex_go/tui"
	"codex_go/tui/keymsg"
	mousemsg "codex_go/tui/mousemsg"
)

func newSidebarReaderSelectionModel(t *testing.T) (*Model, *[]string) {
	t.Helper()
	copies := []string{}
	state := codextui.NewState(nil)
	state.SetThreadID("parent-a")
	state.AddMessage(codextui.RoleUser, "hello from the main chat")
	model := NewModel(state, Options{
		Width:        120,
		Height:       30,
		CopyOnSelect: "always",
		OnSwitchAgent: func(threadID string) (AgentThreadSwitchResponse, error) {
			messages := make([]codextui.Message, 0, 6)
			for index := 0; index < 6; index++ {
				messages = append(messages, codextui.Message{
					Role: codextui.RoleAssistant,
					Text: "agent transcript line " + strconv.Itoa(index),
				})
			}
			return AgentThreadSwitchResponse{
				Entry:    codextui.AgentThreadEntry{ThreadID: threadID, ParentThreadID: "parent-a"},
				Messages: messages,
				Status:   "idle",
			}, nil
		},
		OnClipboardWrite: func(text string) error {
			copies = append(copies, text)
			return nil
		},
		OnPrimarySelectionWrite:   func(string) error { return nil },
		OnPrimarySelectionRelease: func() error { return nil },
		OnSubmit:                  func(string) bubbletea.Cmd { return nil },
	})
	model.featureSettings = map[string]bool{sideWorkspaceFeatureKey: true}
	model.mergeSidebarAgentEntries("parent-a", []codextui.AgentThreadEntry{
		{ThreadID: "parent-a", IsPrimary: true},
		{ThreadID: "agent-1", ParentThreadID: "parent-a", AgentNickname: "Scout", AgentRole: "worker"},
	})
	model.refreshTranscript()
	return model, &copies
}

// Rust #62157 shows the agent in the transcript view, so a drag in the reader
// selects cells and copy-on-select copies them; Escape ends the selection
// before it leaves the reader.
func TestSidebarReaderMouseSelectionCopiesLikeRust(t *testing.T) {
	model, copies := newSidebarReaderSelectionModel(t)
	runTeaCmd(t, model, model.switchToSidebarAgent("agent-1"))
	side := model.activeSide
	if side == nil || !side.WatchOnly {
		t.Fatalf("active side = %#v, want the watch-only reader", side)
	}
	_ = model.viewString()
	left, _, _, ok := model.sidePaneLayout(model.width)
	if !ok {
		t.Fatal("split layout unavailable")
	}
	x := left + sideWorkspaceDividerWidth
	bodyTop := 1
	if model.sidebarTabStripVisible() {
		bodyTop++
	}

	if handled, _ := model.handleSidePaneMouse(mousemsg.MouseMsg{
		Action: mousemsg.MouseActionPress, Button: mousemsg.MouseButtonLeft, X: x, Y: bodyTop,
	}); !handled {
		t.Fatal("press on the reader transcript was not handled")
	}
	if !model.sidebarReaderSelection.active || !model.sidebarReaderFocused {
		t.Fatalf("press selection active=%t focused=%t", model.sidebarReaderSelection.active, model.sidebarReaderFocused)
	}
	model.handleSidePaneMouse(mousemsg.MouseMsg{
		Action: mousemsg.MouseActionMotion, Button: mousemsg.MouseButtonLeft, X: x + 10, Y: bodyTop + 1,
	})
	want := model.sidebarReaderSelectionText(side)
	if !strings.Contains(want, "agent transcript line") {
		t.Fatalf("reader selection text = %q", want)
	}
	if !strings.Contains(model.viewString(), "\x1b[7m") {
		t.Fatal("reader selection was not highlighted")
	}

	_, cmd := model.handleSidePaneMouse(mousemsg.MouseMsg{
		Action: mousemsg.MouseActionRelease, Button: mousemsg.MouseButtonLeft, X: x + 10, Y: bodyTop + 1,
	})
	if cmd == nil {
		t.Fatal("release did not copy the reader selection")
	}
	runTeaCmd(t, model, cmd)
	if len(*copies) != 1 || (*copies)[0] != want {
		t.Fatalf("reader copies = %#v, want %q", *copies, want)
	}

	model.updateSidebarReaderKey(keymsg.KeyMsg{Type: keymsg.KeyEsc, Code: 0x1b})
	if model.sidebarReaderSelection.active {
		t.Fatal("Escape left the reader selection active")
	}
	if !watchOnlySidePane(model.activeSide) {
		t.Fatal("Escape that cleared the selection also closed the reader")
	}
	model.updateSidebarReaderKey(keymsg.KeyMsg{Type: keymsg.KeyEsc, Code: 0x1b})
	if watchOnlySidePane(model.activeSide) {
		t.Fatal("the second Escape kept the reader")
	}
}

// The heading row advertises the reader's back hint, so a press there focuses
// the reader without starting a transcript selection.
func TestSidebarReaderHeadingPressDoesNotSelectLikeRust(t *testing.T) {
	model, _ := newSidebarReaderSelectionModel(t)
	runTeaCmd(t, model, model.switchToSidebarAgent("agent-1"))
	_ = model.viewString()
	left, _, _, ok := model.sidePaneLayout(model.width)
	if !ok {
		t.Fatal("split layout unavailable")
	}
	headingRow := 0
	if model.sidebarTabStripVisible() {
		headingRow = 1
	}
	handled, _ := model.handleSidePaneMouse(mousemsg.MouseMsg{
		Action: mousemsg.MouseActionPress, Button: mousemsg.MouseButtonLeft,
		X: left + sideWorkspaceDividerWidth, Y: headingRow,
	})
	if !handled || !model.sidebarReaderFocused {
		t.Fatalf("heading press handled=%t focused=%t", handled, model.sidebarReaderFocused)
	}
	if model.sidebarReaderSelection.active {
		t.Fatal("heading press started a transcript selection")
	}
}
