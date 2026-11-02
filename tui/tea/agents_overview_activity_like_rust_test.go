package tea

import (
	"testing"

	"codex_go/protocol"
	agentsoverview "codex_go/tui/agents_overview"
)

// Rust #42455: delivered events update the command center's live task details
// without attaching to the task, and turn boundaries clear stale previews.
func TestAgentsOverviewTracksActivityLikeRust(t *testing.T) {
	model := NewModel(nil, Options{
		Width:  120,
		Height: 24,
		OnAgentsOverviewRefresh: func(string) ([]agentsoverview.Row, error) {
			return agentsOverviewTestRows(), nil
		},
	})
	openAgentsDashboard(t, model)
	model.agentsOverview.Rows[1].StatusActive = true
	model.agentsOverview.Rows[1].StatusType = "active"
	if !model.trackAgentsOverviewActivity(protocol.ThreadEvent{
		Type:     "item.reasoning.delta",
		ThreadID: "root-2",
		TurnID:   "turn-1",
		Delta:    &protocol.Delta{ItemID: "reasoning-1", Text: "**Reviewing the patch**"},
	}) {
		t.Fatal("a reasoning delta did not update the live details")
	}
	activity := model.agentsOverview.AgentsOverviewActivityFor("root-2")
	if activity.ReasoningHeading != "Reviewing the patch" {
		t.Fatalf("reasoning heading = %q", activity.ReasoningHeading)
	}
	model.trackAgentsOverviewActivity(protocol.ThreadEvent{
		Type:     "item.completed",
		ThreadID: "root-2",
		Item:     &protocol.ThreadItem{ID: "message-1", Type: "agent_message", Text: "looks good"},
	})
	activity = model.agentsOverview.AgentsOverviewActivityFor("root-2")
	if activity.LastMessage != "looks good" || activity.ReasoningHeading != "" {
		t.Fatalf("activity after completion = %#v", activity)
	}
	model.trackAgentsOverviewActivity(protocol.ThreadEvent{
		Type:     "item.reasoning.delta",
		ThreadID: "root-2",
		TurnID:   "turn-2",
		Delta:    &protocol.Delta{ItemID: "reasoning-2", Text: "**Second pass**"},
	})
	model.trackAgentsOverviewActivity(protocol.ThreadEvent{Type: "turn.completed", ThreadID: "root-2"})
	activity = model.agentsOverview.AgentsOverviewActivityFor("root-2")
	if activity.ReasoningHeading != "" {
		t.Fatalf("stale heading after turn completed = %q", activity.ReasoningHeading)
	}
	if activity.LastMessage != "looks good" {
		t.Fatalf("historical last message lost: %q", activity.LastMessage)
	}
	// Events for tasks the dashboard does not track are ignored.
	if model.trackAgentsOverviewActivity(protocol.ThreadEvent{Type: "turn.completed", ThreadID: "missing"}) {
		t.Fatal("an untracked thread updated the command center")
	}
}
