package agentsoverview

import (
	"strings"
	"testing"
)

func createdSortRows() []Row {
	// Host order and update order intentionally disagree with creation order so
	// each ordering is exercised independently.
	return []Row{
		{ThreadID: "newest-updated", Name: "newest updated", CWD: "/work/a", CreatedAt: 10, UpdatedAt: 90, Group: GroupWorking},
		{ThreadID: "newest-created", Name: "newest created", CWD: "/work/a", CreatedAt: 80, UpdatedAt: 20, Group: GroupReady},
		{ThreadID: "middle", Name: "middle", CWD: "/work/a", CreatedAt: 40, UpdatedAt: 50, Group: GroupWorking},
	}
}

func visibleThreadOrder(view *View) string {
	ids := make([]string, 0, len(view.VisibleIndices()))
	for _, index := range view.VisibleIndices() {
		ids = append(ids, view.Rows[index].ThreadID)
	}
	return strings.Join(ids, ",")
}

// Mirrors Rust #63208: a command center search orders by the selected sort,
// while browsing keeps the last-update order.
func TestAgentsOverviewSearchSortsByCreationLikeRust(t *testing.T) {
	view := New(createdSortRows(), "", false)
	if got := visibleThreadOrder(view); got != "newest-updated,middle,newest-created" {
		t.Fatalf("browsing order = %q, want the last-update order", got)
	}

	view.State.Searching = true
	view.State.SearchSort = SearchSortCreated
	if got := visibleThreadOrder(view); got != "newest-created,middle,newest-updated" {
		t.Fatalf("created search order = %q, want newest created first", got)
	}

	view.State.SearchSort = SearchSortUpdated
	if got := visibleThreadOrder(view); got != "newest-updated,middle,newest-created" {
		t.Fatalf("updated search order = %q, want newest updated first", got)
	}
}

// Mirrors Rust #63208: the status grouping reorders the visible tasks by the
// search sort before the stable status grouping runs.
func TestAgentsOverviewCreatedSortAppliesWithinStatusGroupingLikeRust(t *testing.T) {
	rows := createdSortRows()
	view := New(rows, "", false)
	view.ToggleGrouping() // project -> directory
	view.ToggleGrouping() // directory -> status
	if view.State.Grouping != GroupingStatus {
		t.Fatalf("grouping = %v, want status", view.State.Grouping)
	}
	if got := visibleThreadOrder(view); got != "newest-updated,middle,newest-created" {
		t.Fatalf("browsing status order = %q, want the host recency order", got)
	}

	view.State.Searching = true
	view.State.SearchSort = SearchSortCreated
	// Working tasks keep their relative created order, and the ready task keeps
	// its own group: status grouping dominates the chosen timestamp.
	if got := visibleThreadOrder(view); got != "middle,newest-updated,newest-created" {
		t.Fatalf("created status order = %q", got)
	}
}

// Mirrors Rust #63208: a row without a creation time falls back to its update
// time so the created ordering never hides it.
func TestAgentsOverviewCreatedSortFallsBackToUpdatedLikeRust(t *testing.T) {
	view := New([]Row{
		{ThreadID: "no-created", CWD: "/work/a", UpdatedAt: 60, Group: GroupWorking},
		{ThreadID: "created-old", CWD: "/work/a", CreatedAt: 20, UpdatedAt: 30, Group: GroupWorking},
	}, "", false)
	view.State.Searching = true
	view.State.SearchSort = SearchSortCreated
	if got := visibleThreadOrder(view); got != "no-created,created-old" {
		t.Fatalf("created order with a missing creation time = %q", got)
	}
	if stamp := SearchSortCreated.Timestamp(view.Rows[0]); stamp != 60 {
		t.Fatalf("fallback timestamp = %d, want the update time", stamp)
	}
}

// Mirrors Rust #63208: the footer advertises the Tab sort toggle and its
// current ordering while the search field is active.
func TestAgentsOverviewSearchSortHintLikeRust(t *testing.T) {
	view := New(createdSortRows(), "", false)
	view.State.Searching = true
	view.State.SearchSort = SearchSortCreated
	if footer := renderFooterText(view); !strings.Contains(footer, "sort (Created)") {
		t.Fatalf("footer = %q, want the sort hint", footer)
	}

	view.State.Searching = false
	if footer := renderFooterText(view); strings.Contains(footer, "sort (") {
		t.Fatalf("footer = %q, want no sort hint while browsing", footer)
	}
}

func renderFooterText(view *View) string {
	rows := view.footerHintRows(200)
	var parts []string
	for _, row := range rows {
		var builder strings.Builder
		for _, item := range row {
			builder.WriteString(item.text)
		}
		parts = append(parts, builder.String())
	}
	return strings.Join(parts, "\n")
}
