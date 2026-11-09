package tea

import (
	"testing"

	codextui "codex_go/tui"
	agentsoverview "codex_go/tui/agents_overview"
)

// Mirrors Rust #60286: searching the command center asks the host for history
// matches and publishes tasks that were outside the recent window.
func TestAgentsOverviewSearchPublishesHistoryMatchesLikeRust(t *testing.T) {
	var queried string
	model := NewModel(&codextui.State{}, Options{
		Width: 120, Height: 24,
		OnAgentsOverviewRefresh: func(string) ([]agentsoverview.Row, error) {
			return []agentsoverview.Row{{ThreadID: "recent", Name: "recent task"}}, nil
		},
		OnAgentsOverviewSearch: func(query string) ([]agentsoverview.Row, error) {
			queried = query
			return []agentsoverview.Row{{
				ThreadID: "history-1", Name: "needle task", Preview: "needle", CWD: "/work/needle",
			}}, nil
		},
	})
	openAgentsDashboard(t, model)
	model.agentsOverview.State.Searching = true
	model.agentsOverview.State.Search = "needle"
	command := model.searchAgentsOverviewCmd()
	if command == nil {
		t.Fatal("search did not issue a history request")
	}
	updated, _ := model.Update(command())
	model = updated.(*Model)
	if queried != "needle" {
		t.Fatalf("search query = %q, want needle", queried)
	}
	for _, row := range model.agentsOverview.Rows {
		if row.ThreadID == "history-1" {
			return
		}
	}
	t.Fatalf("history match was not published: %#v", model.agentsOverview.Rows)
}
