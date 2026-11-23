package tea

import (
	"testing"

	codextui "codex_go/tui"
	agentsoverview "codex_go/tui/agents_overview"
)

// Rust #62858: a thread/status/changed notification from another client updates
// the Agents overview row and the agent picker entry in place.
func TestAgentThreadStatusUpdatesDashboardRowsLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 24})
	model.State.ThreadID = "thread-1"
	model.agentsOverview = agentsoverview.New([]agentsoverview.Row{
		{ThreadID: "thread-1", Name: "One", Group: agentsoverview.GroupReady, StatusType: "idle"},
	}, "", false)
	model.agentsOverviewKnownRows = map[string]agentsoverview.Row{
		"thread-1": {ThreadID: "thread-1", Name: "One", Group: agentsoverview.GroupReady, StatusType: "idle"},
	}
	model.agentItems = []codextui.AgentThreadEntry{{ThreadID: "thread-1"}}

	model.applyAgentThreadStatus(AgentThreadStatusMsg{
		ThreadID:          "thread-1",
		StatusType:        "active",
		Active:            true,
		WaitingOnApproval: true,
	})
	row := model.agentsOverview.Rows[0]
	if row.StatusType != "active" || !row.StatusActive || row.Group != agentsoverview.GroupNeedsYou {
		t.Fatalf("row = %#v", row)
	}
	if known := model.agentsOverviewKnownRows["thread-1"]; known.Group != agentsoverview.GroupNeedsYou || !known.StatusActive {
		t.Fatalf("known row = %#v", known)
	}
	if !model.agentItems[0].IsRunning {
		t.Fatal("agent picker entry still reports the thread as idle")
	}

	model.applyAgentThreadStatus(AgentThreadStatusMsg{
		ThreadID:   "thread-1",
		StatusType: "idle",
	})
	if row := model.agentsOverview.Rows[0]; row.Group != agentsoverview.GroupReady || row.StatusActive {
		t.Fatalf("idle row = %#v", row)
	}
	if model.agentItems[0].IsRunning {
		t.Fatal("agent picker entry still reports the thread as running")
	}
}

// A notification for another thread leaves the displayed rows untouched.
func TestAgentThreadStatusIgnoresUnknownThreadsLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 24})
	model.agentsOverview = agentsoverview.New([]agentsoverview.Row{
		{ThreadID: "thread-1", Name: "One", Group: agentsoverview.GroupReady, StatusType: "idle"},
	}, "", false)
	model.applyAgentThreadStatus(AgentThreadStatusMsg{ThreadID: "thread-2", StatusType: "active", Active: true})
	if row := model.agentsOverview.Rows[0]; row.StatusType != "idle" || row.StatusActive {
		t.Fatalf("unrelated row changed: %#v", row)
	}
}
