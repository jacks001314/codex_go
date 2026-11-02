package agentsoverview

import (
	"fmt"
	"strings"
	"testing"
)

func discoveryTestRows(count int) []Row {
	rows := make([]Row, 0, count)
	for i := 0; i < count; i++ {
		rows = append(rows, Row{
			ThreadID:  fmt.Sprintf("task-%02d", i),
			Name:      fmt.Sprintf("task %02d", i),
			CWD:       "/work/a",
			Group:     GroupReady,
			StatusType: "idle",
			UpdatedAt: int64(1000 - i),
		})
	}
	return rows
}

// Rust #49106: the command center seeds ten tasks, and each "Show more"
// activation publishes ten more until the retained history is exhausted.
func TestAgentsOverviewHistoryPaginationLikeRust(t *testing.T) {
	view := New(discoveryTestRows(25), "", false)
	view.SetHistoryPaging(true)
	if limit := view.HistoryLimit(); limit != AgentsOverviewPageSize {
		t.Fatalf("history limit = %d", limit)
	}
	if visible := len(view.VisibleIndices()); visible != 10 {
		t.Fatalf("visible tasks = %d, want 10", visible)
	}
	if !view.HasMore() {
		t.Fatal("more history should be available")
	}
	selectable := view.SelectableIndices()
	if len(selectable) != 11 || selectable[len(selectable)-1] != ShowMoreIndex {
		t.Fatalf("selectable indices = %v", selectable)
	}
	if added, hasMore := view.NextBatch(); added != 10 || !hasMore {
		t.Fatalf("first batch = %d more=%v", added, hasMore)
	}
	if visible := len(view.VisibleIndices()); visible != 20 {
		t.Fatalf("visible tasks after one batch = %d", visible)
	}
	if added, hasMore := view.NextBatch(); added != 5 || hasMore {
		t.Fatalf("second batch = %d more=%v", added, hasMore)
	}
	if len(view.SelectableIndices()) != len(view.VisibleIndices()) {
		t.Fatal("the Show more row survived the last page")
	}
}

// Rust #49106: the "Show more" row is keyboard-selectable, renders its
// loading/retry label, stays reachable during search, and suppresses duplicate
// page requests while loading.
func TestAgentsOverviewShowMoreSelectionLikeRust(t *testing.T) {
	view := New(discoveryTestRows(15), "", false)
	view.SetHistoryPaging(true)
	view.JumpBottom()
	if !view.ShowMoreSelected() || view.SelectedThreadID() != "" {
		t.Fatalf("bottom selection = %d showMore=%v", view.Selected, view.ShowMoreSelected())
	}
	rendered := strings.Join(view.Render(100, 30), "\n")
	if !strings.Contains(rendered, "Show more") {
		t.Fatalf("Show more row missing:\n%s", rendered)
	}
	if action := view.Activate(); action != ActionShowMore {
		t.Fatalf("activating Show more returned %v", action)
	}
	if !view.ShowMoreLoading() || view.ShowMoreLabel() != "Loading more\u2026" {
		t.Fatalf("loading state = %v label=%q", view.ShowMoreLoading(), view.ShowMoreLabel())
	}
	if action, ok := view.ShowMoreAgentsOverview(); ok || action != ActionNone {
		t.Fatal("a duplicate page request was not suppressed")
	}
	view.SetShowMoreFailed(true)
	if view.ShowMoreLabel() != "Show more (retry)" {
		t.Fatalf("retry label = %q", view.ShowMoreLabel())
	}
	// The row is still selectable while searching.
	view.State.Searching = true
	view.State.Search = "task 0"
	selectable := view.SelectableIndices()
	if len(selectable) == 0 || selectable[len(selectable)-1] != ShowMoreIndex {
		t.Fatalf("searchable indices = %v", selectable)
	}
}

// Rust #49106: a page request that finds fewer rows than the page size ends
// paging and moves the selection back onto a real task.
func TestAgentsOverviewShowMoreEndsPagingLikeRust(t *testing.T) {
	view := New(discoveryTestRows(12), "", false)
	view.SetHistoryPaging(true)
	view.JumpBottom()
	if action := view.Activate(); action != ActionShowMore {
		t.Fatalf("Show more action = %v", action)
	}
	if added, hasMore := view.NextBatch(); added != 2 || hasMore {
		t.Fatalf("final batch = %d more=%v", added, hasMore)
	}
	if view.ShowMoreSelected() {
		t.Fatal("selection stayed on the removed Show more row")
	}
	if row := view.SelectedRow(); row == nil {
		t.Fatal("selection did not return to a task")
	}
}
