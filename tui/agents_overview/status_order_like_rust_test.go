package agentsoverview

import (
	"strings"
	"testing"
)

// statusRow builds one dashboard row for a status-grouping fixture.
func statusRow(threadID string, group Group) Row {
	return Row{ThreadID: threadID, Name: threadID, Group: group}
}

// Mirrors Rust #52946 live_center_keeps_status_order_across_progress_refreshes
// and the row_order sort in AgentsOverviewView::new: a progress refresh
// re-supplies the same tasks in a new recency order, and every task keeps its
// relative position while it stays in the same status group.
func TestStatusOrderRetainedAcrossProgressRefreshesLikeRust(t *testing.T) {
	view := New(nil, "", false)
	view.State.Grouping = GroupingStatus

	view.ApplyRefresh([]Row{
		statusRow("alpha", GroupWorking),
		statusRow("bravo", GroupWorking),
		statusRow("charlie", GroupWorking),
		statusRow("ready", GroupReady),
		statusRow("needs", GroupNeedsYou),
	}, "bravo")
	want := "needs,alpha,bravo,charlie,ready"
	if got := strings.Join(visibleThreadIDs(view), ","); got != want {
		t.Fatalf("initial status order = %s, want %s", got, want)
	}

	// Progress updates change the host's recency order; the displayed order and
	// the selection must not move with it.
	for step, rows := range [][]Row{
		{
			statusRow("charlie", GroupWorking),
			statusRow("ready", GroupReady),
			statusRow("needs", GroupNeedsYou),
			statusRow("bravo", GroupWorking),
			statusRow("alpha", GroupWorking),
		},
		{
			statusRow("bravo", GroupWorking),
			statusRow("alpha", GroupWorking),
			statusRow("charlie", GroupWorking),
			statusRow("needs", GroupNeedsYou),
			statusRow("ready", GroupReady),
		},
	} {
		view.ApplyRefresh(rows, view.SelectedThreadID())
		if got := strings.Join(visibleThreadIDs(view), ","); got != want {
			t.Fatalf("refresh %d status order = %s, want %s", step, got, want)
		}
		if selected := view.SelectedThreadID(); selected != "bravo" {
			t.Fatalf("refresh %d selected = %q, want bravo", step, selected)
		}
	}
}

// Mirrors Rust #52946 live_center_appends_arrivals_and_status_transitions: a new
// task and a task that changed status group are appended after the rows already
// in their destination group, in the order the host supplied.
func TestStatusOrderAppendsArrivalsAndTransitionsLikeRust(t *testing.T) {
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

	// delta is a new arrival in Working; charlie moves from Working to Ready.
	view.ApplyRefresh([]Row{
		statusRow("delta", GroupWorking),
		statusRow("charlie", GroupReady),
		statusRow("alpha", GroupWorking),
		statusRow("bravo", GroupWorking),
		statusRow("ready", GroupReady),
	}, "")
	want := "alpha,bravo,delta,ready,charlie"
	if got := strings.Join(visibleThreadIDs(view), ","); got != want {
		t.Fatalf("arrivals and transitions order = %s, want %s", got, want)
	}
}

// Mirrors Rust #52946 "forget the order of removed tasks so rediscovered tasks
// behave like new arrivals".
func TestStatusOrderForgetsRemovedRowsLikeRust(t *testing.T) {
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
	// bravo is rediscovered, so it is appended instead of returning to its old
	// position behind alpha.
	if got := strings.Join(visibleThreadIDs(view), ","); got != "alpha,charlie,bravo" {
		t.Fatalf("rediscovered order = %s, want alpha,charlie,bravo", got)
	}
}

// Mirrors Rust #52946 "search and status filters preserve the retained order,
// and pinned tasks continue to use their explicit ordering".
func TestStatusOrderPreservesFiltersAndPinsLikeRust(t *testing.T) {
	view := New(nil, "", false)
	view.State.Grouping = GroupingStatus
	view.ApplyRefresh([]Row{
		{ThreadID: "alpha", Name: "Alpha", Group: GroupWorking, Source: "cli"},
		{ThreadID: "bravo", Name: "Bravo", Group: GroupWorking, Source: "cli"},
		{ThreadID: "charlie", Name: "Charlie", Group: GroupWorking, Source: "cli"},
	}, "")

	// The search filter hides bravo; the survivors keep the retained order.
	view.State.Search = "l"
	if got := strings.Join(visibleThreadIDs(view), ","); got != "alpha,charlie" {
		t.Fatalf("filtered order = %s, want alpha,charlie", got)
	}
	view.State.Search = ""

	// Pinned tasks keep their shared-section order ahead of the retained rows.
	view.SetPinnedThreads([]string{"charlie"})
	if got := strings.Join(visibleThreadIDs(view), ","); got != "charlie,alpha,bravo" {
		t.Fatalf("pinned order = %s, want charlie,alpha,bravo", got)
	}
	view.ClearPinnedThreads()
	view.ApplyRefresh([]Row{
		statusRow("charlie", GroupWorking),
		statusRow("bravo", GroupWorking),
		statusRow("alpha", GroupWorking),
	}, "")
	if got := strings.Join(visibleThreadIDs(view), ","); got != "alpha,bravo,charlie" {
		t.Fatalf("post-unpin order = %s, want alpha,bravo,charlie", got)
	}
}
