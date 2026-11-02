package agentsoverview

import (
	"strings"
	"testing"
)

// statusRow builds one dashboard row for a status-grouping fixture.
func statusRow(threadID string, group Group) Row {
	return Row{ThreadID: threadID, Name: threadID, Group: group}
}

// Mirrors Rust #58714 live_center_keeps_status_order_across_progress_refreshes:
// the caller supplies rows in descending update order, so a progress refresh
// reorders tasks inside their status group.
func TestStatusOrderTracksRecencyAcrossProgressRefreshesLikeRust(t *testing.T) {
	view := New(nil, "", false)
	view.State.Grouping = GroupingStatus

	view.ApplyRefresh([]Row{
		statusRow("alpha", GroupWorking),
		statusRow("bravo", GroupWorking),
		statusRow("charlie", GroupWorking),
		statusRow("ready", GroupReady),
		statusRow("needs", GroupNeedsYou),
	}, "bravo")
	if got := strings.Join(visibleThreadIDs(view), ","); got != "needs,alpha,bravo,charlie,ready" {
		t.Fatalf("initial status order = %s", got)
	}

	// Update timestamps changed, so the host re-supplies rows in a new order.
	view.ApplyRefresh([]Row{
		statusRow("charlie", GroupWorking),
		statusRow("ready", GroupReady),
		statusRow("needs", GroupNeedsYou),
		statusRow("bravo", GroupWorking),
		statusRow("alpha", GroupWorking),
	}, "bravo")
	if got := strings.Join(visibleThreadIDs(view), ","); got != "needs,charlie,bravo,alpha,ready" {
		t.Fatalf("refresh status order = %s, want needs,charlie,bravo,alpha,ready", got)
	}
	if selected := view.SelectedThreadID(); selected != "bravo" {
		t.Fatalf("selected = %q, want bravo", selected)
	}
}

// Mirrors Rust #58714 live_center_sorts_arrivals_and_status_transitions_by_update_time.
func TestStatusOrderSortsArrivalsAndTransitionsByRecencyLikeRust(t *testing.T) {
	view := New(nil, "", false)
	view.State.Grouping = GroupingStatus
	view.ApplyRefresh([]Row{
		statusRow("alpha", GroupWorking),
		statusRow("bravo", GroupWorking),
		statusRow("charlie", GroupWorking),
		statusRow("ready", GroupReady),
	}, "")
	if got := strings.Join(visibleThreadIDs(view), ","); got != "alpha,bravo,charlie,ready" {
		t.Fatalf("initial status order = %s", got)
	}

	// delta is a new working arrival and charlie moved to Ready; both carry
	// their new update times.
	view.ApplyRefresh([]Row{
		statusRow("delta", GroupWorking),
		statusRow("charlie", GroupReady),
		statusRow("alpha", GroupWorking),
		statusRow("bravo", GroupWorking),
		statusRow("ready", GroupReady),
	}, "")
	want := "delta,alpha,bravo,charlie,ready"
	if got := strings.Join(visibleThreadIDs(view), ","); got != want {
		t.Fatalf("arrivals and transitions order = %s, want %s", got, want)
	}
}

// Mirrors Rust #58714: a rediscovered task returns to its position by update
// time instead of being appended as a new arrival.
func TestStatusOrderRestoresRediscoveredRowsByRecencyLikeRust(t *testing.T) {
	view := New(nil, "", false)
	view.State.Grouping = GroupingStatus
	view.ApplyRefresh([]Row{
		statusRow("alpha", GroupWorking),
		statusRow("bravo", GroupWorking),
		statusRow("charlie", GroupWorking),
	}, "")
	view.ApplyRefresh([]Row{
		statusRow("alpha", GroupWorking),
		statusRow("charlie", GroupWorking),
	}, "")
	view.ApplyRefresh([]Row{
		statusRow("bravo", GroupWorking),
		statusRow("alpha", GroupWorking),
		statusRow("charlie", GroupWorking),
	}, "")
	if got := strings.Join(visibleThreadIDs(view), ","); got != "bravo,alpha,charlie" {
		t.Fatalf("rediscovered order = %s, want bravo,alpha,charlie", got)
	}
}

// Mirrors Rust #58714 live_center_status_order_tracks_updates_while_filtered_and_pinned:
// filters do not freeze hidden timestamps, and pins lead their status group in
// pin order without creating a separate Pinned group.
func TestStatusOrderTracksFiltersAndPinsLikeRust(t *testing.T) {
	view := New(nil, "", false)
	view.State.Grouping = GroupingStatus
	view.ApplyRefresh([]Row{
		{ThreadID: "alpha", Name: "Alpha", Group: GroupWorking, Source: "cli"},
		{ThreadID: "bravo", Name: "Bravo", Group: GroupWorking, Source: "cli"},
		{ThreadID: "charlie", Name: "Charlie", Group: GroupWorking, Source: "cli"},
	}, "")

	view.State.Search = "l"
	if got := strings.Join(visibleThreadIDs(view), ","); got != "alpha,charlie" {
		t.Fatalf("filtered order = %s, want alpha,charlie", got)
	}
	view.State.Search = ""

	view.ApplyRefresh([]Row{
		{ThreadID: "charlie", Name: "Charlie", Group: GroupWorking, Source: "cli"},
		{ThreadID: "bravo", Name: "Bravo", Group: GroupWorking, Source: "cli"},
		{ThreadID: "alpha", Name: "Alpha", Group: GroupWorking, Source: "cli"},
	}, "")
	if got := strings.Join(visibleThreadIDs(view), ","); got != "charlie,bravo,alpha" {
		t.Fatalf("post-filter order = %s, want charlie,bravo,alpha", got)
	}

	view.SetPinnedThreads([]string{"alpha", "charlie"})
	if got := strings.Join(visibleThreadIDs(view), ","); got != "alpha,charlie,bravo" {
		t.Fatalf("pinned order = %s, want alpha,charlie,bravo", got)
	}
	pinnedIndex := -1
	for index := range view.Rows {
		if view.Rows[index].ThreadID == "alpha" {
			pinnedIndex = index
			break
		}
	}
	if pinnedIndex < 0 {
		t.Fatal("alpha row missing")
	}
	if heading := view.groupHeading(GroupingStatus, pinnedIndex); heading != GroupWorking.Label() {
		t.Fatalf("pinned heading = %q, want %q", heading, GroupWorking.Label())
	}
	var rendered strings.Builder
	for _, item := range view.renderRowSpans(pinnedIndex, false, 80) {
		rendered.WriteString(item.text)
	}
	if !strings.Contains(rendered.String(), "📌 Alpha") {
		t.Fatalf("pinned row = %q, want the 📌 title marker", rendered.String())
	}

	view.ClearPinnedThreads()
	if got := strings.Join(visibleThreadIDs(view), ","); got != "charlie,bravo,alpha" {
		t.Fatalf("post-unpin order = %s, want charlie,bravo,alpha", got)
	}
}
