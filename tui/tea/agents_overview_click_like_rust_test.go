package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	agentsoverview "codex_go/tui/agents_overview"
	mousemsg "codex_go/tui/mousemsg"
	"codex_go/utils"
)

func agentsOverviewClickModel(t *testing.T) *Model {
	t.Helper()
	state := codextui.NewState(nil)
	state.SetThreadID("source")
	model := NewModel(state, Options{
		Width:  120,
		Height: 24,
		OnSwitchAgent: func(threadID string) (AgentThreadSwitchResponse, error) {
			return AgentThreadSwitchResponse{
				Entry:  codextui.AgentThreadEntry{ThreadID: threadID},
				Status: "idle",
			}, nil
		},
	})
	model.agentsOverview = agentsoverview.New([]agentsoverview.Row{
		{ThreadID: "alpha", Name: "alpha", CWD: "work", Model: "gpt-test"},
		{ThreadID: "beta", Name: "beta", CWD: "work", Model: "gpt-test"},
	}, "alpha", false)
	return model
}

func agentsOverviewRowFor(t *testing.T, rendered string, needle string) int {
	t.Helper()
	for index, line := range strings.Split(rendered, "\n") {
		if strings.Contains(utils.StripANSI(line), needle) {
			return index
		}
	}
	t.Fatalf("row %q not painted:\n%s", needle, rendered)
	return -1
}

// Rust #62990 click_row: a click on a painted task selects it and opens it even
// when another row owned the selection.
func TestAgentsOverviewClickOpensPaintedTaskLikeRust(t *testing.T) {
	model := agentsOverviewClickModel(t)
	rendered := model.renderAgentsOverview()
	row := agentsOverviewRowFor(t, rendered, "beta")

	_, cmd := model.Update(mousemsg.MouseMsg{
		X: 6, Y: row, Action: mousemsg.MouseActionPress, Button: mousemsg.MouseButtonLeft,
	})
	if cmd == nil {
		t.Fatal("click on a task row produced no open command")
	}
	if got := model.agentsOverview.SelectedThreadID(); got != "beta" {
		t.Fatalf("selection = %q, want the clicked task", got)
	}
	if got := model.agentsOverview.OpeningThread(); got != "beta" {
		t.Fatalf("opening = %q, want the clicked task", got)
	}
	if model.agentsOverview.Focus() != agentsoverview.AgentsOverviewFocusList {
		t.Fatalf("focus = %v, want the task list", model.agentsOverview.Focus())
	}
}

// A click outside the painted rows leaves the dashboard alone.
func TestAgentsOverviewClickOutsideRowsKeepsSelectionLikeRust(t *testing.T) {
	model := agentsOverviewClickModel(t)
	model.renderAgentsOverview()
	_, cmd := model.Update(mousemsg.MouseMsg{
		X: 1, Y: 0, Action: mousemsg.MouseActionPress, Button: mousemsg.MouseButtonLeft,
	})
	if cmd != nil {
		t.Fatalf("header click produced a command: %v", cmd)
	}
	if got := model.agentsOverview.SelectedThreadID(); got != "alpha" {
		t.Fatalf("selection = %q, want the original row", got)
	}
	if got := model.agentsOverview.OpeningThread(); got != "" {
		t.Fatalf("header click opened %q", got)
	}
}

// While the help overlay or a metadata editor owns the dashboard, clicks keep
// their input (Rust click_row guards the same states).
func TestAgentsOverviewClickIgnoredWhileEditingLikeRust(t *testing.T) {
	model := agentsOverviewClickModel(t)
	row := agentsOverviewRowFor(t, model.renderAgentsOverview(), "beta")
	model.agentsOverview.State.Help = true
	model.renderAgentsOverview()
	_, cmd := model.Update(mousemsg.MouseMsg{
		X: 6, Y: row, Action: mousemsg.MouseActionPress, Button: mousemsg.MouseButtonLeft,
	})
	if cmd != nil {
		t.Fatalf("help overlay click produced a command: %v", cmd)
	}
	if got := model.agentsOverview.SelectedThreadID(); got != "alpha" {
		t.Fatalf("help overlay click changed the selection to %q", got)
	}
	if got := model.agentsOverview.OpeningThread(); got != "" {
		t.Fatalf("help overlay click opened %q", got)
	}
}
