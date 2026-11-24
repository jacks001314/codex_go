package tea

import (
	"strconv"
	"testing"

	bubbletea "charm.land/bubbletea/v2"

	codextui "codex_go/tui"
	keymsg "codex_go/tui/keymsg"
	mousemsg "codex_go/tui/mousemsg"
)

// newSidebarReaderFocusModel builds the watch-only reader over a transcript
// long enough that the pane can actually scroll.
func newSidebarReaderFocusModel(t *testing.T) *Model {
	t.Helper()
	state := codextui.NewState(nil)
	state.SetThreadID("parent-a")
	state.AddMessage(codextui.RoleUser, "hello from the main chat")
	model := NewModel(state, Options{
		Width:  120,
		Height: 30,
		OnSwitchAgent: func(threadID string) (AgentThreadSwitchResponse, error) {
			messages := make([]codextui.Message, 0, 80)
			for index := 0; index < 80; index++ {
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
		OnCloseSide: func(SideCloseParams) (SideCloseResponse, error) {
			return SideCloseResponse{}, nil
		},
		OnSubmit: func(string) bubbletea.Cmd { return nil },
	})
	model.featureSettings = map[string]bool{sideWorkspaceFeatureKey: true}
	model.mergeSidebarAgentEntries("parent-a", []codextui.AgentThreadEntry{
		{ThreadID: "parent-a", IsPrimary: true},
		{ThreadID: "agent-1", ParentThreadID: "parent-a", AgentNickname: "Scout", AgentRole: "worker"},
	})
	model.refreshTranscript()
	return model
}

// Rust #62157 handle_docked_agent_input: while the watch-only reader owns
// input, the pager keys scroll its transcript instead of reaching the parent
// composer, and Escape returns to the Subagents list.
func TestSidebarReaderPaneFocusScrollsTranscriptLikeRust(t *testing.T) {
	model := newSidebarReaderFocusModel(t)
	runTeaCmd(t, model, model.switchToSidebarAgent("agent-1"))
	side := model.activeSide
	if side == nil || !side.WatchOnly {
		t.Fatalf("active side = %#v, want the watch-only reader", side)
	}
	_ = model.viewString()
	top := side.SideViewMax
	if top <= 0 {
		t.Fatalf("reader transcript never scrolled: SideViewMax = %d", top)
	}
	if model.sidebarReaderFocused {
		t.Fatal("reader claimed input before the user focused it")
	}

	left, _, _, ok := model.sidePaneLayout(model.width)
	if !ok {
		t.Fatal("split layout unavailable")
	}
	handled, cmd := model.handleSidePaneMouse(mousemsg.MouseMsg{
		Action: mousemsg.MouseActionPress, Button: mousemsg.MouseButtonLeft,
		X: left + sideWorkspaceDividerWidth, Y: 3,
	})
	if !handled {
		t.Fatal("press on the reader was not handled")
	}
	runTeaCmd(t, model, cmd)
	if !model.sidebarReaderFocused {
		t.Fatal("press on the reader did not give it focus")
	}

	model.Update(keymsg.KeyMsg{Type: keymsg.KeyUp})
	if side.SideScroll != top-1 {
		t.Fatalf("first Up scrolled to %d, want %d", side.SideScroll, top-1)
	}
	page := max(model.transcript.Height-1, 1)
	if top-1-page < 0 {
		t.Fatalf("transcript too short to page: top=%d page=%d", top, page)
	}
	model.Update(keymsg.KeyMsg{Type: keymsg.KeyPgUp})
	if side.SideScroll != top-1-page {
		t.Fatalf("PgUp scrolled to %d, want %d", side.SideScroll, top-1-page)
	}
	model.Update(keymsg.KeyMsg{Type: keymsg.KeyPgDown})
	if side.SideScroll != top-1 {
		t.Fatalf("PgDown scrolled to %d, want %d", side.SideScroll, top-1)
	}
	model.Update(keymsg.KeyMsg{Type: keymsg.KeyDown})
	if side.SideScroll != top {
		t.Fatalf("Down scrolled to %d, want %d", side.SideScroll, top)
	}

	// Ordinary typing belongs to the reader, so the parent composer keeps its
	// draft. Rust paints no composer in a watch-only reader.
	model.Update(keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune{'x'}, Text: "x"})
	if got := model.composerText(); got != "" {
		t.Fatalf("parent composer received %q from the focused reader", got)
	}

	_, cmd = model.Update(keymsg.KeyMsg{Type: keymsg.KeyEsc, Code: 0x1b})
	runTeaCmd(t, model, cmd)
	if model.sidebarReaderFocused {
		t.Fatal("Escape kept the reader's input focus")
	}
	if !model.sidebarChooserVisible() || model.activeSide != nil {
		t.Fatalf("Escape left chooser=%t side=%#v, want the Subagents list", model.sidebarChooserVisible(), model.activeSide)
	}
}

// Rust #62157 side_focus: the sidebar-content shortcut focuses the docked
// reader that is already open instead of replacing it with the chooser.
func TestSidebarFocusContentKeepsOpenReaderLikeRust(t *testing.T) {
	model := newSidebarReaderFocusModel(t)
	runTeaCmd(t, model, model.switchToSidebarAgent("agent-1"))
	if !watchOnlySidePane(model.activeSide) {
		t.Fatalf("active side = %#v, want the watch-only reader", model.activeSide)
	}

	runTeaCmd(t, model, model.focusSidebarContent())
	if !model.sidebarReaderFocused {
		t.Fatal("focusing sidebar content did not focus the reader")
	}
	if model.sidebarChooserVisible() {
		t.Fatal("focusing sidebar content replaced the reader with the list")
	}

	model.closeSidebarSubagentTranscript(model.activeSide)
	if model.sidebarReaderFocused {
		t.Fatal("closing the reader kept its input focus")
	}
	if model.activeSide != nil {
		t.Fatalf("reader survived its close: %#v", model.activeSide)
	}
}
