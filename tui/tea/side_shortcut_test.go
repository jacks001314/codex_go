package tea

import (
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

func TestCtrlSlashTerminalEncodingTogglesSideConversation(t *testing.T) {
	// Most terminals and ConPTY deliver Ctrl+/ as the C0 unit-separator byte
	// (0x1f). Windows conhost instead reports the raw key with a NUL Char,
	// which bubbletea surfaces as KeyRunes with a single NUL rune. Both
	// encodings must toggle the side conversation.
	encodings := []bubbletea.KeyMsg{
		{Type: bubbletea.KeyCtrlUnderscore},
		{Type: bubbletea.KeyRunes, Runes: []rune{0}},
	}
	for i, ctrlSlash := range encodings {
		state := codextui.NewState(nil)
		state.SetThreadID("thread-side")
		state.AddMessage(codextui.RoleAssistant, "side answer")
		model := NewModel(state, Options{})
		model.activeSide = &activeSideConversation{
			ParentThreadID: "thread-parent",
			SideThreadID:   "thread-side",
			ParentMessages: []codextui.Message{{Role: codextui.RoleUser, Text: "main question"}},
			SideMessages:   cloneSideMessages(state.Messages),
			ShowingSide:    true,
		}

		if got := keySpecFromKeyMsg(ctrlSlash); got != "ctrl-7" {
			t.Fatalf("encoding %d: Ctrl+/ normalized to %q, want ctrl-7", i, got)
		}

		model.Update(ctrlSlash)
		if state.ThreadID != "thread-parent" || model.activeSide.ShowingSide {
			t.Fatalf("encoding %d: first Ctrl+/ left thread=%q showingSide=%t", i, state.ThreadID, model.activeSide.ShowingSide)
		}
		if len(state.Messages) != 1 || state.Messages[0].Text != "main question" {
			t.Fatalf("encoding %d: parent transcript = %#v", i, state.Messages)
		}

		model.Update(ctrlSlash)
		if state.ThreadID != "thread-side" || !model.activeSide.ShowingSide {
			t.Fatalf("encoding %d: second Ctrl+/ left thread=%q showingSide=%t", i, state.ThreadID, model.activeSide.ShowingSide)
		}
		if len(state.Messages) != 1 || state.Messages[0].Text != "side answer" {
			t.Fatalf("encoding %d: side transcript = %#v", i, state.Messages)
		}
	}
}
