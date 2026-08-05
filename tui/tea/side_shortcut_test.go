package tea

import (
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

func TestCtrlSlashTerminalEncodingTogglesSideConversation(t *testing.T) {
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

	ctrlSlash := bubbletea.KeyMsg{Type: bubbletea.KeyCtrlUnderscore}
	if got := keySpecFromKeyMsg(ctrlSlash); got != "ctrl-7" {
		t.Fatalf("Ctrl+/ terminal encoding normalized to %q, want ctrl-7", got)
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
