package tea

import (
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	"codex_go/tui/agents_overview"
)

func TestDeleteCurrentSessionReturnsToCommandCenterOnSharedServerLikeRust(t *testing.T) {
	for _, shared := range []bool{false, true} {
		state := codextui.NewState(nil)
		state.SetThreadID("thread-delete")
		var action codextui.SessionSelection
		model := NewModel(state, Options{
			Width:              100,
			Height:             30,
			LocalDaemonSession: shared,
			OnSessionAction: func(selection codextui.SessionSelection) (*codextui.SessionSummary, error) {
				action = selection
				return nil, nil
			},
			OnReadAgents: func(string) ([]codextui.AgentThreadEntry, error) {
				return []codextui.AgentThreadEntry{{ThreadID: "thread-other"}}, nil
			},
			OnAgentsOverviewRefresh: func(string) ([]agentsoverview.Row, error) {
				return nil, nil
			},
		})
		model.openCurrentSessionActionConfirmation(codextui.SessionSelectionDelete)
		if model.modal == nil || len(model.modal.options) != 2 {
			t.Fatalf("delete confirmation = %#v", model.modal)
		}
		if shared && model.modal.options[1].Label != "Yes, delete and return to command center" {
			t.Fatalf("shared delete label = %q", model.modal.options[1].Label)
		}
		if !shared && model.modal.options[1].Label != "Yes, delete and exit" {
			t.Fatalf("embedded delete label = %q", model.modal.options[1].Label)
		}
		model.Update(key(bubbletea.KeyDown))
		_, cmd := model.Update(key(bubbletea.KeyEnter))
		if cmd == nil {
			t.Fatal("delete confirmation returned no command")
		}
		if action.Kind != codextui.SessionSelectionDelete || action.Target.ThreadID != "thread-delete" {
			t.Fatalf("session action = %#v", action)
		}
		if shared {
			if batchContainsMessageType(cmd, bubbletea.Quit()) {
				t.Fatal("shared-server delete must not quit the TUI")
			}
			runTeaCmd(t, model, cmd)
			if model.agentsOverview == nil {
				t.Fatal("shared-server delete did not return to the command center")
			}
		} else if !batchContainsMessageType(cmd, bubbletea.Quit()) {
			t.Fatal("embedded delete must exit the TUI")
		}
	}
}
