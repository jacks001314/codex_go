package agentsoverview

import (
	"strings"
	"testing"
)

func activityTestView() *View {
	return New([]Row{
		{ThreadID: "root-1", Name: "alpha", CWD: "/work/a", Group: GroupWorking, StatusType: "active", StatusActive: true},
		{ThreadID: "root-2", Name: "beta", CWD: "/work/a", Group: GroupNeedsYou, StatusType: "active", StatusActive: true, WaitingOnApproval: true},
	}, "", false)
}

// Rust #42455: a streamed reasoning summary contributes its first bold heading
// as the task's live activity, and later deltas neither extend nor duplicate it.
func TestAgentsOverviewActivityReasoningHeadingLikeRust(t *testing.T) {
	view := activityTestView()
	event := AgentsOverviewEvent{
		Kind:   AgentsOverviewEventReasoningDelta,
		TurnID: "turn-1",
		ItemID: "reasoning-1",
		Delta:  "Reading the ",
	}
	if changed := view.TrackAgentsOverviewActivity("root-1", event); changed {
		t.Fatal("a delta without a heading should not repaint")
	}
	event.Delta = "**parser** modules"
	if changed := view.TrackAgentsOverviewActivity("root-1", event); !changed {
		t.Fatal("the heading appearing did not repaint")
	}
	activity := view.AgentsOverviewActivityFor("root-1")
	if activity.ReasoningHeading != "parser" {
		t.Fatalf("reasoning heading = %q", activity.ReasoningHeading)
	}
	event.Delta = " more"
	if changed := view.TrackAgentsOverviewActivity("root-1", event); changed {
		t.Fatal("a delta after the heading repainted")
	}
}

// Rust #42455: the latest delivered agent message preview collapses whitespace
// and replacing it clears the stale reasoning heading.
func TestAgentsOverviewActivityLastMessageLikeRust(t *testing.T) {
	view := activityTestView()
	view.TrackAgentsOverviewActivity("root-1", AgentsOverviewEvent{
		Kind: AgentsOverviewEventReasoningDelta, ItemID: "reasoning-1", Delta: "**Working**",
	})
	if !view.TrackAgentsOverviewActivity("root-1", AgentsOverviewEvent{
		Kind: AgentsOverviewEventAgentMessage, Text: "  done\nwith\tit  ",
	}) {
		t.Fatal("a completed agent message did not repaint")
	}
	activity := view.AgentsOverviewActivityFor("root-1")
	if activity.LastMessage != "  done with it  " {
		t.Fatalf("last message = %q", activity.LastMessage)
	}
	if activity.ReasoningHeading != "" {
		t.Fatalf("stale reasoning heading = %q", activity.ReasoningHeading)
	}
	long := strings.Repeat("x", AgentsOverviewActivityPreviewChars+10)
	view.TrackAgentsOverviewActivity("root-1", AgentsOverviewEvent{Kind: AgentsOverviewEventAgentMessage, Text: long})
	if got := len([]rune(view.AgentsOverviewActivityFor("root-1").LastMessage)); got != AgentsOverviewActivityPreviewChars {
		t.Fatalf("bounded last message length = %d", got)
	}
}

// Rust #42455: only tasks the dashboard tracks receive previews, and turn
// boundaries clear a stale reasoning heading.
func TestAgentsOverviewActivityBoundariesLikeRust(t *testing.T) {
	view := activityTestView()
	if view.TrackAgentsOverviewActivity("unknown", AgentsOverviewEvent{Kind: AgentsOverviewEventAgentMessage, Text: "x"}) {
		t.Fatal("an untracked thread was updated")
	}
	view.TrackAgentsOverviewActivity("root-1", AgentsOverviewEvent{
		Kind: AgentsOverviewEventReasoningDelta, ItemID: "reasoning-1", Delta: "**Working**",
	})
	if !view.TrackAgentsOverviewActivity("root-1", AgentsOverviewEvent{Kind: AgentsOverviewEventTurnCompleted}) {
		t.Fatal("a turn boundary did not clear live activity")
	}
	if heading := view.AgentsOverviewActivityFor("root-1").ReasoningHeading; heading != "" {
		t.Fatalf("heading after turn boundary = %q", heading)
	}
}

// Rust #42455: the details pane leads with the pending request for a
// needs-attention task and shows the live activity of a running task.
func TestAgentsOverviewDetailsShowLiveActivityLikeRust(t *testing.T) {
	view := activityTestView()
	view.SetAgentsOverviewRequestPreview("root-2", "Run `go test ./...`?")
	view.Selected = 1
	rendered := strings.Join(view.Render(120, 34), "\n")
	for _, want := range []string{"Needs attention", "Run `go test ./...`?", "Open task to review.", "Waiting for approval."} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("details missing %q:\n%s", want, rendered)
		}
	}
	view.Selected = 0
	view.TrackAgentsOverviewActivity("root-1", AgentsOverviewEvent{
		Kind: AgentsOverviewEventReasoningDelta, ItemID: "reasoning-1", Delta: "**Reading files**",
	})
	rendered = strings.Join(view.Render(120, 34), "\n")
	for _, want := range []string{"Latest activity", "Reading files"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("details missing %q:\n%s", want, rendered)
		}
	}
}
