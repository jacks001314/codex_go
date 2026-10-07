package tea

import (
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

func TestCtrlSlashTerminalEncodingTogglesSideConversation(t *testing.T) {
	// Ctrl+/ is delivered as the C0 unit-separator byte (0x1f) on both the ANSI
	// byte path and Windows conhost; bubbletea exposes that byte as
	// KeyCtrlUnderscore and it must normalize to the Rust Ctrl+7 alias and
	// toggle the side conversation. A NUL byte/char is Ctrl+Space, not Ctrl+/
	// (see keymap_ctrl_space_test.go), so it is not part of this encoding list.
	ctrlSlash := bubbletea.KeyMsg{Type: bubbletea.KeyCtrlUnderscore}
	if got := keySpecFromKeyMsg(ctrlSlash); got != "ctrl-7" {
		t.Fatalf("Ctrl+/ normalized to %q, want ctrl-7", got)
	}

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

	model.Update(ctrlSlash)
	if state.ThreadID != "thread-parent" || model.activeSide.ShowingSide {
		t.Fatalf("first Ctrl+/ left thread=%q showingSide=%t", state.ThreadID, model.activeSide.ShowingSide)
	}
	if len(state.Messages) != 1 || state.Messages[0].Text != "main question" {
		t.Fatalf("parent transcript = %#v", state.Messages)
	}

	model.Update(ctrlSlash)
	if state.ThreadID != "thread-side" || !model.activeSide.ShowingSide {
		t.Fatalf("second Ctrl+/ left thread=%q showingSide=%t", state.ThreadID, model.activeSide.ShowingSide)
	}
	if len(state.Messages) != 1 || state.Messages[0].Text != "side answer" {
		t.Fatalf("side transcript = %#v", state.Messages)
	}
}
