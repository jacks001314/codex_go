package tea

import (
	"testing"

	agentsoverview "codex_go/tui/agents_overview"
	"codex_go/tui/keymsg"
)

// Rust #44360 returned Escape from the overview list to its inline composer.
// Upstream #45255 replaced that composer with direct new-session shortcuts, so
// the current baseline lets ListAction::Cancel complete the view even while a
// connection notice is shown. Go has no overview composer either; Escape closes
// the dashboard without quitting or switching tasks.
func TestModelAgentsDashboardEscapeClosesWhileReconnectingLikeRust(t *testing.T) {
	model := NewModel(nil, Options{
		Width:  120,
		Height: 24,
		OnAgentsOverviewRefresh: func(currentThreadID string) ([]agentsoverview.Row, error) {
			return agentsOverviewTestRows(), nil
		},
	})
	openAgentsDashboard(t, model)
	model.agentsOverview.MoveSelection(true)
	model.agentsOverviewNotice = "Reconnecting\u2026"
	model.agentsOverview.Notice = "Reconnecting\u2026"

	updated, command := model.Update(key(keymsg.KeyEsc))
	model = updated.(*Model)
	if command != nil {
		t.Fatalf("escape during reconnect returned command %T, want nil", command)
	}
	if model.agentsOverview != nil {
		t.Fatal("escape during reconnect did not close the dashboard")
	}
}
