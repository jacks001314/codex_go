package tea

import (
	"testing"
	"time"

	bubbletea "charm.land/bubbletea/v2"

	codextui "codex_go/tui"
	agentsoverview "codex_go/tui/agents_overview"
)

// Rust #62858: the visible Agents overview re-reads its inventory every 10
// seconds, so a thread another client changed cannot leave the dashboard stale
// when this TUI holds no conversation stream for it.
func TestAgentsOverviewPollsInventoryWhileVisibleLikeRust(t *testing.T) {
	previous := agentsOverviewPollInterval
	agentsOverviewPollInterval = time.Millisecond
	t.Cleanup(func() { agentsOverviewPollInterval = previous })

	refreshCalls := 0
	model := NewModel(nil, Options{
		Width:  120,
		Height: 24,
		OnAgentsOverviewRefresh: func(string) ([]agentsoverview.Row, error) {
			refreshCalls++
			return nil, nil
		},
	})
	if cmd := model.agentsOverviewPollCmd(); cmd != nil {
		t.Fatal("a closed dashboard scheduled an inventory poll")
	}
	if cmd := model.applyAgentsCommand(); cmd == nil {
		t.Fatal("opening the dashboard produced no refresh")
	}
	model.applyAgentsOverviewList(agentsOverviewListMsg{rows: nil, backendID: model.backendID, requestID: model.agentsOverviewRefresh})
	if model.agentsOverviewPolling {
		t.Fatal("a model without an event loop started an inventory poll")
	}
	// A running TUI has the program's message loop attached to its frames.
	model.frames.attach(func(bubbletea.Msg) {})
	model.applyAgentsOverviewList(agentsOverviewListMsg{rows: nil, backendID: model.backendID, requestID: model.agentsOverviewRefresh})
	if !model.agentsOverviewPolling {
		t.Fatal("the visible dashboard did not start its inventory poll")
	}
	if cmd := model.startAgentsOverviewPoll(); cmd != nil {
		t.Fatal("a completed refresh started a second poll chain")
	}

	updated, cmd := model.Update(agentsOverviewPollMsg{})
	model = updated.(*Model)
	if cmd == nil {
		t.Fatal("an inventory poll produced no work")
	}
	batch, ok := cmd().(bubbletea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("poll command = %#v, want a refresh plus the next tick", batch)
	}
	for range collectBatchMsgs(cmd) {
	}
	if refreshCalls != 1 {
		t.Fatalf("poll refreshed %d times, want 1", refreshCalls)
	}

	model.closeAgentsOverview()
	if cmd := model.agentsOverviewPollCmd(); cmd != nil {
		t.Fatal("closing the dashboard kept polling")
	}
	if cmd := model.applyAgentsOverviewPoll(); cmd != nil {
		t.Fatal("a poll on a closed dashboard produced work")
	}
	if model.agentsOverviewPolling {
		t.Fatal("a poll on a closed dashboard kept the chain marked as running")
	}
}

// Rust #62858: a fresh listing restores a task this client tombstoned while it
// missed the unarchive, and keeps an in-flight archive hidden.
func TestAgentsOverviewFreshListingRecoversMissedUnarchiveLikeRust(t *testing.T) {
	backend := codextui.NewBackendID()
	model := NewModel(nil, Options{
		Width:  120,
		Height: 24,
		// A server-owned backend hosts Cloud conversations, so its listing is
		// the fresh Cloud inventory page Rust #62858 trusts over a tombstone.
		BackendID:           backend,
		ServerOwnedSettings: true,
		OnAgentsOverviewRefresh: func(string) ([]agentsoverview.Row, error) {
			return nil, nil
		},
	})
	if cmd := model.applyAgentsCommand(); cmd == nil {
		t.Fatal("opening the dashboard produced no refresh")
	}
	model.agentsOverviewRemoved = map[string]bool{"root-restored": true, "root-archiving": true}
	model.pendingCurrentArchives = map[string]*pendingCurrentArchive{"root-archiving": {}}

	model.applyAgentsOverviewList(agentsOverviewListMsg{
		rows: []agentsoverview.Row{
			{ThreadID: "root-restored", Name: "Restored", StatusType: "idle"},
			{ThreadID: "root-archiving", Name: "Archiving", StatusType: "idle"},
		},
		backendID: model.backendID,
		requestID: model.agentsOverviewRefresh,
	})

	if model.agentsOverviewRemoved["root-restored"] {
		t.Fatal("a listed task stayed tombstoned after a missed unarchive")
	}
	if !model.agentsOverviewRemoved["root-archiving"] {
		t.Fatal("a task with a pending archive lost its removal")
	}
	visible := false
	for _, row := range model.agentsOverview.Rows {
		if row.ThreadID == "root-restored" {
			visible = true
		}
		if row.ThreadID == "root-archiving" {
			t.Fatal("a task with a pending archive reappeared in the dashboard")
		}
	}
	if !visible {
		t.Fatalf("the restored task is missing from the dashboard rows: %#v", model.agentsOverview.Rows)
	}
}
