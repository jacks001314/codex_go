package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

func TestClearCommandClearsTerminalAndStartsFreshSessionLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name          string
		overlayActive bool
		want          string
	}{
		{name: "inline scrollback", want: "\x1b[2J\x1b[3J\x1b[H"},
		{name: "alternate overlay", overlayActive: true, want: "\x1b[2J\x1b[H"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			state := codextui.NewState(nil)
			state.SetThreadID("thread-before-clear")
			state.AddMessage(codextui.RoleUser, "old transcript")

			var sequences []string
			model := NewModel(state, Options{
				ShowSessionHeader: true,
				OnClearTerminal: func(sequence string) bubbletea.Cmd {
					sequences = append(sequences, sequence)
					return func() bubbletea.Msg { return nil }
				},
			})
			model.overlayAltScreen = testCase.overlayActive

			typeText(t, model, "/clear")
			model.Update(key(bubbletea.KeyEnter))

			if len(sequences) != 1 || sequences[0] != testCase.want {
				t.Fatalf("terminal clear sequences = %#v, want %q", sequences, testCase.want)
			}
			if state.ThreadID != "" || len(state.Messages) != 1 || state.Status != "idle" {
				t.Fatalf("clear state = thread %q messages %d status %q", state.ThreadID, len(state.Messages), state.Status)
			}
			// Rust #48562 re-inserts the compact borderless header after /clear.
			if view := model.View(); !strings.Contains(view, ">_ OpenAI Codex (vdev)") || strings.Contains(view, "No messages yet.") {
				t.Fatalf("View() missing fresh session header:\n%s", view)
			}
		})
	}
}

func TestNewCommandDoesNotClearTerminalLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-before-new")

	var sequences []string
	model := NewModel(state, Options{
		OnClearTerminal: func(sequence string) bubbletea.Cmd {
			sequences = append(sequences, sequence)
			return func() bubbletea.Msg { return nil }
		},
	})

	typeText(t, model, "/new")
	model.Update(key(bubbletea.KeyEnter))

	if len(sequences) != 0 {
		t.Fatalf("terminal clear sequences = %#v, want none", sequences)
	}
}

// Rust #58266: Ctrl+L (global clear_terminal) clears the painted screen without
// discarding the transcript, the draft, or the session.
func TestCtrlLClearsTerminalAndPreservesTranscriptLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-before-clear-screen")
	state.AddMessage(codextui.RoleUser, "old transcript")

	var sequences []string
	model := NewModel(state, Options{
		OnClearTerminal: func(sequence string) bubbletea.Cmd {
			sequences = append(sequences, sequence)
			return func() bubbletea.Msg { return nil }
		},
	})
	if !model.keyMatches("global", "clear_terminal", "ctrl-l") {
		t.Fatal("ctrl-l is not bound to global clear_terminal")
	}
	typeText(t, model, "unsent draft")
	model.Update(key(bubbletea.KeyCtrlL))

	if len(sequences) != 1 || sequences[0] != "\x1b[2J\x1b[3J\x1b[H" {
		t.Fatalf("terminal clear sequences = %#v", sequences)
	}
	if state.ThreadID != "thread-before-clear-screen" || len(state.Messages) != 1 {
		t.Fatalf("ctrl-l changed the session: thread=%q messages=%d", state.ThreadID, len(state.Messages))
	}
	if got := model.composerText(); got != "unsent draft" {
		t.Fatalf("ctrl-l cleared the draft: %q", got)
	}
	if !strings.Contains(model.View(), "old transcript") {
		t.Fatalf("ctrl-l dropped the transcript:\n%s", model.View())
	}
}

func TestClearCommandRejectsPendingPermissionRootSwitchLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-before-clear")
	state.AddMessage(codextui.RoleUser, "old transcript")

	var sequences []string
	model := NewModel(state, Options{
		OnClearTerminal: func(sequence string) bubbletea.Cmd {
			sequences = append(sequences, sequence)
			return func() bubbletea.Msg { return nil }
		},
	})
	model.pendingServerProfile = "trusted"

	typeText(t, model, "/clear")
	model.Update(key(bubbletea.KeyEnter))

	if len(sequences) != 0 {
		t.Fatalf("terminal clear sequences = %#v, want none", sequences)
	}
	if state.ThreadID != "thread-before-clear" || len(state.Messages) < 1 {
		t.Fatalf("clear changed thread state while permissions were pending: thread %q messages %d", state.ThreadID, len(state.Messages))
	}
	if !strings.Contains(model.View(), "Wait for permissions to update before switching tasks.") {
		t.Fatalf("View() missing pending-permission error:\n%s", model.View())
	}
}
