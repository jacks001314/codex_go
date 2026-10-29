package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	agentsoverview "codex_go/tui/agents_overview"
	"codex_go/utils"
)

// Mirrors Rust #45983: opening a task from the agents overview must give the
// user feedback immediately, keep it while the attach is in flight, and replace
// it once the chat widget has been rebuilt. Bubbletea runs the switch on a
// command goroutine and never clears the terminal, so Go raises the footer
// notice instead of Rust's full-screen bold `Loading task…` frame.
func TestModelAgentsDashboardOpenShowsSwitchNoticeLikeRust(t *testing.T) {
	model := NewModel(nil, Options{
		Width:  120,
		Height: 24,
		OnAgentsOverviewRefresh: func(string) ([]agentsoverview.Row, error) {
			return agentsOverviewTestRows(), nil
		},
		OnSwitchAgent: func(threadID string) (AgentThreadSwitchResponse, error) {
			return AgentThreadSwitchResponse{
				Entry:  codextui.AgentThreadEntry{ThreadID: threadID, AgentNickname: "agent"},
				Status: "idle",
			}, nil
		},
	})
	openAgentsDashboard(t, model)
	model.agentsOverview.Selected = 1 // root-2
	updated, command := model.Update(key(bubbletea.KeyEnter))
	model = updated.(*Model)
	if command == nil {
		t.Fatal("open returned no attach command")
	}

	pendingNotice := model.notice
	if !strings.HasPrefix(pendingNotice, "Switching to ") {
		t.Fatalf("pending attach notice = %q, want a switching notice", pendingNotice)
	}
	if output := utils.StripANSI(model.View()); !strings.Contains(output, pendingNotice) {
		t.Fatalf("pending attach notice is not rendered:\n%s", output)
	}

	updated, _ = model.Update(command())
	model = updated.(*Model)
	if strings.HasPrefix(model.notice, "Switching to ") {
		t.Fatalf("notice was not replaced after the widget rebuild: %q", model.notice)
	}
}
