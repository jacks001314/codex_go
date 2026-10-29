package tea

import (
	"testing"

	agentsoverview "codex_go/tui/agents_overview"
)

func agentsOverviewRetainedRowIDs(t *testing.T, model *Model) []string {
	t.Helper()
	if model == nil || model.agentsOverview == nil {
		t.Fatal("agents overview is not open")
	}
	ids := make([]string, 0, len(model.agentsOverview.Rows))
	for _, row := range model.agentsOverview.Rows {
		ids = append(ids, row.ThreadID)
	}
	return ids
}

func refreshAgentsOverviewForTest(t *testing.T, model *Model) *Model {
	t.Helper()
	command := model.refreshAgentsOverviewCmd()
	if command == nil {
		t.Fatal("refreshAgentsOverviewCmd() = nil")
	}
	updated, _ := model.Update(command())
	return updated.(*Model)
}

// TestAgentsOverviewRetainsDiscoveredSessionsLikeRust pins Rust #42104: the
// command center keeps sessions discovered during this TUI's lifetime even
// after they leave the server's loaded/recent listing, and renders them as
// unloaded instead of dropping the row.
func TestAgentsOverviewRetainsDiscoveredSessionsLikeRust(t *testing.T) {
	rows := agentsOverviewTestRows()
	model := NewModel(nil, Options{
		Width:  120,
		Height: 24,
		OnAgentsOverviewRefresh: func(string) ([]agentsoverview.Row, error) {
			return rows, nil
		},
	})
	openAgentsDashboard(t, model)

	// The next listing only carries root-1: root-2 and root-3 aged out of the
	// loaded/recent window but stay in the dashboard's retained membership.
	rows = []agentsoverview.Row{{
		ThreadID:     "root-1",
		Name:         "alpha renamed",
		Preview:      "fix parser",
		CWD:          "/work/a",
		Group:        agentsoverview.GroupWorking,
		StatusType:   "active",
		StatusActive: true,
	}}
	model = refreshAgentsOverviewForTest(t, model)
	ids := agentsOverviewRetainedRowIDs(t, model)
	if len(ids) != 3 || ids[0] != "root-1" || ids[1] != "root-2" || ids[2] != "root-3" {
		t.Fatalf("retained row order = %v, want [root-1 root-2 root-3]", ids)
	}
	if got := model.agentsOverview.Rows[0].Name; got != "alpha renamed" {
		t.Fatalf("fresh row name = %q, want the refreshed metadata", got)
	}
	retained := model.agentsOverview.Rows[1]
	if retained.StatusType != "notLoaded" || retained.StatusActive || retained.Group != agentsoverview.GroupFinished {
		t.Fatalf("retained row = %#v, want an unloaded finished row", retained)
	}

	// Archive/delete tombstones still remove a retained row from the next
	// refresh; retained membership must not resurrect it.
	model.agentsOverviewRemoved = map[string]bool{"root-2": true}
	model = refreshAgentsOverviewForTest(t, model)
	ids = agentsOverviewRetainedRowIDs(t, model)
	if len(ids) != 2 || ids[0] != "root-1" || ids[1] != "root-3" {
		t.Fatalf("rows after tombstone = %v, want [root-1 root-3]", ids)
	}
}
