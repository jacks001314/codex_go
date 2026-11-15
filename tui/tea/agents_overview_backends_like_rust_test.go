package tea

import (
	"fmt"
	"testing"

	codextui "codex_go/tui"
	agentsoverview "codex_go/tui/agents_overview"
)

func agentsOverviewBackendTestModel(t *testing.T, rows []agentsoverview.Row) (*Model, codextui.BackendID, codextui.BackendID) {
	t.Helper()
	model := NewModel(nil, Options{
		Width:  120,
		Height: 24,
		OnAgentsOverviewRefresh: func(string) ([]agentsoverview.Row, error) {
			return rows, nil
		},
		OnAgentsOverviewTogglePin: func(string, bool) error { return nil },
	})
	backendA := codextui.NewBackendID()
	backendB := codextui.NewBackendID()
	model.backendID = backendA
	if cmd := model.applyAgentsCommand(); cmd == nil {
		t.Fatal("opening the dashboard produced no refresh")
	}
	// The opening page is delivered below so the retained membership starts
	// from the test rows.
	model.cancelAgentsOverviewRefresh(backendA)
	if cmd := model.refreshAgentsOverviewCmd(); cmd == nil {
		t.Fatal("initial refresh produced no command")
	} else if message, ok := cmd().(agentsOverviewListMsg); !ok {
		t.Fatal("refresh did not return a list message")
	} else {
		model.applyAgentsOverviewList(message)
	}
	return model, backendA, backendB
}

// TestAgentsOverviewQueuesRefreshesPerBackendLikeRust covers Rust #61596: a
// refresh requested while the shared slot is busy is queued for its own
// backend, and switching backends preserves that work instead of dropping it.
func TestAgentsOverviewQueuesRefreshesPerBackendLikeRust(t *testing.T) {
	model, backendA, backendB := agentsOverviewBackendTestModel(t, agentsOverviewTestRows())

	first := model.refreshAgentsOverviewCmd()
	if first == nil {
		t.Fatal("first refresh produced no command")
	}
	if second := model.refreshAgentsOverviewCmd(); second != nil {
		t.Fatal("duplicate refresh for the same backend was not coalesced")
	} else if state := model.agentsOverviewBackendPeek(backendA); state == nil || !state.pending {
		t.Fatalf("backend A pending = %#v", state)
	}

	// Switch to B while A's read is still pending. B's refresh must queue for
	// later without stealing A's slot.
	model.backendID = backendB
	if queued := model.refreshAgentsOverviewCmd(); queued != nil {
		t.Fatal("refresh for another backend took the busy slot")
	} else if state := model.agentsOverviewBackendPeek(backendB); state == nil || !state.pending {
		t.Fatalf("backend B pending = %#v", state)
	}

	message, ok := first().(agentsOverviewListMsg)
	if !ok {
		t.Fatal("first refresh returned a non-list message")
	}
	drained := model.applyAgentsOverviewList(message)
	if drained == nil {
		t.Fatal("completing A did not drain B's queued refresh")
	}
	if state := model.agentsOverviewBackendPeek(backendA); state == nil || state.failed {
		t.Fatalf("backend A state = %#v", state)
	}
}

// TestAgentsOverviewCancelledPageDoesNotApplyLikeRust covers the stale-read
// half of Rust #61596: a page started before a mutation or disconnect must not
// replace the window or consume the backend's refill budget.
func TestAgentsOverviewCancelledPageDoesNotApplyLikeRust(t *testing.T) {
	model, backendA, _ := agentsOverviewBackendTestModel(t, agentsOverviewTestRows())
	knownBefore := len(model.agentsOverviewKnownRows)

	cmd := model.refreshAgentsOverviewCmd()
	if cmd == nil {
		t.Fatal("refresh produced no command")
	}
	model.cancelAgentsOverviewRefresh(backendA)
	if model.agentsOverviewInflight {
		t.Fatal("cancel left the shared refresh slot occupied")
	}
	message := cmd().(agentsOverviewListMsg)
	if got := model.applyAgentsOverviewList(message); got != nil {
		t.Fatal("a cancelled page started another refresh")
	}
	if got := len(model.agentsOverviewKnownRows); got != knownBefore {
		t.Fatalf("cancelled page changed retained rows: %d -> %d", knownBefore, got)
	}
}

// TestAgentsOverviewRefillAndShowMoreStayPerBackendLikeRust covers the refill
// accounting in Rust #61596: a removal fills from the owning backend's budget,
// a "Show more" request belongs to the backend that asked for it, and another
// backend's page cannot spend that budget.
func TestAgentsOverviewRefillAndShowMoreStayPerBackendLikeRust(t *testing.T) {
	rows := make([]agentsoverview.Row, 0, 15)
	for index := 0; index < 15; index++ {
		rows = append(rows, agentsoverview.Row{
			ThreadID: fmt.Sprintf("root-%02d", index),
			Name:     fmt.Sprintf("task-%02d", index),
			Group:    agentsoverview.GroupReady,
		})
	}
	model, backendA, backendB := agentsOverviewBackendTestModel(t, rows)
	model.agentsOverview.SetHistoryLimit(10)
	model.agentsOverview.RefreshHistoryPaging()
	if !model.agentsOverview.HasMore() {
		t.Fatal("test rows did not leave a paged tail")
	}

	model.noteThreadBackend("root-00", backendA)
	model.noteAgentsOverviewRemovedThread("root-00")
	model.agentsOverview.SetHistoryLimit(10)
	model.agentsOverview.RefreshHistoryPaging()
	stateA := model.agentsOverviewBackendPeek(backendA)
	if stateA == nil || stateA.refill != 1 {
		t.Fatalf("backend A refill = %#v", stateA)
	}

	// With a pending refill, "Show more" is satisfied by the refill instead of
	// queuing an explicit page request.
	model.showMoreAgentsOverviewCmd()
	if stateA.showMore {
		t.Fatal("Show more queued despite a pending refill")
	}

	// A page from backend B must not consume A's vacancy.
	model.applyAgentsOverviewList(agentsOverviewListMsg{
		backendID: backendB,
		requestID: model.agentsOverviewRefresh,
		rows:      append(append([]agentsoverview.Row(nil), rows...), agentsoverview.Row{ThreadID: "root-extra", Name: "extra", Group: agentsoverview.GroupReady}),
	})
	if stateA.refill != 1 {
		t.Fatalf("backend A refill after backend B page = %d", stateA.refill)
	}

	// A's own page now fills the vacancy.
	model.applyAgentsOverviewList(agentsOverviewListMsg{
		backendID: backendA,
		requestID: model.agentsOverviewRefresh,
		rows:      append(append([]agentsoverview.Row(nil), rows...), agentsoverview.Row{ThreadID: "root-fill", Name: "fill", Group: agentsoverview.GroupReady}),
	})
	if stateA.refill != 0 {
		t.Fatalf("backend A refill after its own page = %d", stateA.refill)
	}

	// Without a refill, Show more is queued for the requesting backend.
	model.agentsOverview.SetHistoryLimit(10)
	model.agentsOverview.RefreshHistoryPaging()
	if !model.agentsOverview.HasMore() {
		t.Fatal("test rows no longer leave a paged tail")
	}
	model.showMoreAgentsOverviewCmd()
	if !stateA.showMore {
		t.Fatal("Show more was not recorded for its backend")
	}
}

// TestAgentsOverviewDisconnectHidesShowMorePerBackendLikeRust covers the
// availability half of Rust #61596: a disconnected backend exposes no
// pagination, while switching back to a connected backend restores its state.
func TestAgentsOverviewDisconnectHidesShowMorePerBackendLikeRust(t *testing.T) {
	model, backendA, backendB := agentsOverviewBackendTestModel(t, agentsOverviewTestRows())
	stateA := model.agentsOverviewBackendState(backendA)
	stateA.hasMore = true
	model.syncAgentsOverviewBackendView()
	if !model.agentsOverview.HasMore() {
		t.Fatal("connected backend did not expose Show more")
	}

	stateA.disconnected = true
	model.cancelAgentsOverviewRefresh(backendA)
	if model.agentsOverview.HasMore() {
		t.Fatal("disconnected backend still exposed Show more")
	}

	stateB := model.agentsOverviewBackendState(backendB)
	stateB.hasMore = true
	model.backendID = backendB
	model.syncAgentsOverviewBackendView()
	if !model.agentsOverview.HasMore() {
		t.Fatal("connected destination backend did not restore Show more")
	}
}

// TestAgentsOverviewPinCompletionScopedToBackendLikeRust covers the pin
// completion change in Rust #61596.
func TestAgentsOverviewPinCompletionScopedToBackendLikeRust(t *testing.T) {
	model, backendA, backendB := agentsOverviewBackendTestModel(t, agentsOverviewTestRows())
	model.backendID = backendA
	if cmd := model.toggleAgentsOverviewPinCmd("root-1", true); cmd == nil {
		t.Fatal("pin command was not created")
	}
	if model.agentsOverviewPinBackend != backendA {
		t.Fatalf("pin owner = %v, want %v", model.agentsOverviewPinBackend, backendA)
	}
	if cmd := model.applyAgentsOverviewPin(agentsOverviewPinMsg{threadID: "root-1", pinned: true, backendID: backendB}); cmd != nil {
		t.Fatal("pin completion from another backend started a refresh")
	}
	if model.agentsOverviewPinBackend != backendA {
		t.Fatal("foreign pin completion cleared the owning backend")
	}
	model.applyAgentsOverviewPin(agentsOverviewPinMsg{threadID: "root-1", pinned: true, backendID: backendA})
	if !model.agentsOverviewPinBackend.IsZero() {
		t.Fatal("owning backend pin completion was not consumed")
	}
}
