package tea

import (
	"testing"

	codextui "codex_go/tui"
	agentsoverview "codex_go/tui/agents_overview"
	"codex_go/tui/keymsg"
)

// Mirrors Rust #63208: Tab inside Command Center search switches between the
// last-update and creation-time orderings and rescans history in the new order.
func TestAgentsOverviewSearchTabTogglesSortLikeRust(t *testing.T) {
	var orders []agentsoverview.SearchSort
	model := NewModel(&codextui.State{}, Options{
		Width: 120, Height: 24,
		OnAgentsOverviewRefresh: func(string) ([]agentsoverview.Row, error) {
			return []agentsoverview.Row{{ThreadID: "recent", Name: "recent task", CreatedAt: 1, UpdatedAt: 2}}, nil
		},
		OnAgentsOverviewSearch: func(query string, order agentsoverview.SearchSort) ([]agentsoverview.Row, error) {
			orders = append(orders, order)
			return []agentsoverview.Row{{ThreadID: "history-1", Name: "needle task", Preview: "needle", CWD: "/work/needle", CreatedAt: 3, UpdatedAt: 4}}, nil
		},
	})
	openAgentsDashboard(t, model)
	model.agentsOverview.State.Searching = true
	model.agentsOverview.State.Search = "needle"
	if order := model.agentsOverview.State.SearchSort; order != agentsoverview.SearchSortUpdated {
		t.Fatalf("initial search sort = %v, want updated", order)
	}

	updated, command := model.Update(key(keymsg.KeyTab))
	model = updated.(*Model)
	if model.agentsOverview == nil {
		t.Fatal("Tab closed the dashboard")
	}
	if order := model.agentsOverview.State.SearchSort; order != agentsoverview.SearchSortCreated {
		t.Fatalf("search sort after Tab = %v, want created", order)
	}
	if command == nil {
		t.Fatal("Tab did not rescan history after changing the sort")
	}
	updated, _ = model.Update(command())
	model = updated.(*Model)
	if len(orders) != 1 || orders[0] != agentsoverview.SearchSortCreated {
		t.Fatalf("history scans = %v, want one created-time scan", orders)
	}
	if model.agentsOverview.State.Search != "needle" {
		t.Fatalf("search query changed to %q", model.agentsOverview.State.Search)
	}

	updated, command = model.Update(key(keymsg.KeyTab))
	model = updated.(*Model)
	if order := model.agentsOverview.State.SearchSort; order != agentsoverview.SearchSortUpdated {
		t.Fatalf("search sort after second Tab = %v, want updated", order)
	}
	updated, _ = model.Update(command())
	model = updated.(*Model)
	if len(orders) != 2 || orders[1] != agentsoverview.SearchSortUpdated {
		t.Fatalf("history scans = %v, want a second updated-time scan", orders)
	}
}

// Mirrors Rust #63208: Tab keeps cycling the status filter when the search
// field is not active.
func TestAgentsOverviewTabKeepsCyclingFiltersOutsideSearchLikeRust(t *testing.T) {
	model := NewModel(&codextui.State{}, Options{
		Width: 120, Height: 24,
		OnAgentsOverviewRefresh: func(string) ([]agentsoverview.Row, error) {
			return []agentsoverview.Row{{ThreadID: "recent", Name: "recent task"}}, nil
		},
	})
	openAgentsDashboard(t, model)
	before := model.agentsOverview.State.StatusFilter
	model.Update(key(keymsg.KeyTab))
	if model.agentsOverview.State.SearchSort != agentsoverview.SearchSortUpdated {
		t.Fatal("browsing Tab must not change the search ordering")
	}
	if model.agentsOverview.State.StatusFilter == before {
		t.Fatal("browsing Tab must still cycle the status filter")
	}
}
