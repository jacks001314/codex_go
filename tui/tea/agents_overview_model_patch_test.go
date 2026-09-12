package tea

import (
	"testing"

	"codex_go/appserver"
	agentsoverview "codex_go/tui/agents_overview"
)

// TestModelThreadScopedSettingsPatchesDashboardRow covers Rust #44957: a model
// change reported for a non-active listed task patches the command center's row
// without waiting for the next thread-list refresh.
func TestModelThreadScopedSettingsPatchesDashboardRow(t *testing.T) {
	model := NewModel(nil, Options{
		Width:  120,
		Height: 24,
		OnAgentsOverviewRefresh: func(string) ([]agentsoverview.Row, error) {
			return agentsOverviewTestRows(), nil
		},
	})
	openAgentsDashboard(t, model)
	model.Update(ThreadScopedSettingsUpdatedMsg{
		ThreadID: "root-2",
		Settings: appserver.Settings{Model: "gpt-6-astra"},
	})
	var patched string
	for _, row := range model.agentsOverview.Rows {
		if row.ThreadID == "root-2" {
			patched = row.Model
		}
	}
	if patched != "gpt-6-astra" {
		t.Fatalf("row model = %q, want the patched model", patched)
	}

	// Without the dashboard the message is a no-op.
	closed := NewModel(nil, Options{Width: 120, Height: 24})
	if command := closed.applyThreadScopedSettingsUpdated(ThreadScopedSettingsUpdatedMsg{
		ThreadID: "root-2",
		Settings: appserver.Settings{Model: "gpt-6-astra"},
	}); command != nil {
		t.Fatal("a settings update without the dashboard must not return a command")
	}
}
