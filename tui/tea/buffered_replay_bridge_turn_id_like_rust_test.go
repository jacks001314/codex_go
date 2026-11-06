package tea

import (
	"strings"
	"testing"

	"codex_go/protocol"
	codextui "codex_go/tui"
	"codex_go/utils"
)

// bridgeShapedDelegatedReplayEvents mirrors what app/remote_tui.go actually
// sends for an inactive thread: the item notifications are built with
// protocol.AgentMessageDelta / protocol.ItemCompleted / protocol.ItemStarted,
// none of which carry a TurnID. Only turn.started names the turn.
func bridgeShapedDelegatedReplayEvents(threadID string) []protocol.ThreadEvent {
	return []protocol.ThreadEvent{
		protocol.TurnStartedWithID("voice-turn"),
		protocol.ItemCompleted(*delegationItem("user-1")),
		protocol.AgentMessageDelta("commentary-1", "private commentary"),
		protocol.ItemCompleted(protocol.AgentMessageItemWithPhase("commentary-1", "private commentary", "commentary")),
		protocol.ItemCompleted(protocol.ThreadItem{ID: "reasoning-1", Type: "reasoning", Summary: []string{"private reasoning"}}),
		protocol.AgentMessageDelta("agent-1", "the public answer"),
		protocol.ItemCompleted(protocol.AgentMessageItemWithPhase("agent-1", "the public answer", "final_answer")),
	}
}

// TestBufferedReplayAttributesBridgeItemsToTurnLikeRust reproduces Rust
// ThreadEventStore::snapshot for a background voice-delegated thread using the
// real bridge event shapes (Rust 95327467c3 / hide_private_items_after_voice_handoff).
// The buffered reducer must attribute TurnID-less item notifications to the turn
// opened by turn.started; otherwise the delegation marker is never recorded and
// the delegated turn's private reasoning/commentary replays into the transcript.
func TestBufferedReplayAttributesBridgeItemsToTurnLikeRust(t *testing.T) {
	model := newRealtimeDelegationTestModel(t)
	model.backgroundThreadEvents["thread-worker"] = bridgeShapedDelegatedReplayEvents("thread-worker")
	model.applyAgentSwitchResult(AgentSwitchResultMsg{
		ThreadID: "thread-worker",
		Response: AgentThreadSwitchResponse{
			Entry:  codextui.AgentThreadEntry{ThreadID: "thread-worker"},
			Status: "idle",
		},
	})

	view := utils.StripANSI(model.viewString())
	for _, leaked := range []string{"private commentary", "private reasoning"} {
		if strings.Contains(view, leaked) {
			t.Fatalf("delegated voice content %q leaked into the replayed transcript:\n%s", leaked, view)
		}
	}
	if !strings.Contains(view, "the public answer") {
		t.Fatalf("public answer missing after the replay:\n%s", view)
	}
	if !strings.Contains(view, "inspect the workspace") {
		t.Fatalf("spoken prompt missing after the replay:\n%s", view)
	}
	if strings.Contains(view, "<realtime_delegation>") {
		t.Fatalf("delegation envelope rendered instead of the spoken prompt:\n%s", view)
	}
}

// TestBufferedReplayKeepsTypedBridgeEventsLikeRust is the control case: the
// same TurnID-less event shape on a typed turn must not be suppressed.
func TestBufferedReplayKeepsTypedBridgeEventsLikeRust(t *testing.T) {
	model := newRealtimeDelegationTestModel(t)
	model.backgroundThreadEvents["thread-worker"] = []protocol.ThreadEvent{
		protocol.TurnStartedWithID("typed-turn"),
		protocol.ItemCompleted(protocol.ThreadItem{ID: "user-1", Type: "user_message", Text: "typed question"}),
		protocol.AgentMessageDelta("commentary-1", "typed commentary"),
		protocol.ItemCompleted(protocol.AgentMessageItemWithPhase("commentary-1", "typed commentary", "commentary")),
	}
	model.applyAgentSwitchResult(AgentSwitchResultMsg{
		ThreadID: "thread-worker",
		Response: AgentThreadSwitchResponse{
			Entry:  codextui.AgentThreadEntry{ThreadID: "thread-worker"},
			Status: "idle",
		},
	})

	view := utils.StripANSI(model.viewString())
	if !strings.Contains(view, "typed commentary") || !strings.Contains(view, "typed question") {
		t.Fatalf("typed bridge-shaped replay changed:\n%s", view)
	}
}
