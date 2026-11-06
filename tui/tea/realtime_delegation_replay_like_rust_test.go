package tea

import (
	"strings"
	"testing"

	"codex_go/protocol"
	codextui "codex_go/tui"
	"codex_go/utils"
)

func delegatedReplayEvents() []protocol.ThreadEvent {
	return []protocol.ThreadEvent{
		{Type: "turn.started", ThreadID: "thread-worker", TurnID: "voice-turn"},
		{Type: "item.completed", ThreadID: "thread-worker", TurnID: "voice-turn", Item: delegationItem("user-1")},
		{Type: "item.delta", ThreadID: "thread-worker", TurnID: "voice-turn", Delta: &protocol.Delta{ItemID: "commentary-1", Text: "private commentary"}},
		{Type: "item.completed", ThreadID: "thread-worker", TurnID: "voice-turn", Item: &protocol.ThreadItem{ID: "commentary-1", Type: "agent_message", Phase: "commentary", Text: "private commentary"}},
		{Type: "item.completed", ThreadID: "thread-worker", TurnID: "voice-turn", Item: &protocol.ThreadItem{ID: "reasoning-1", Type: "reasoning", Summary: []string{"private reasoning"}}},
		{Type: "item.delta", ThreadID: "thread-worker", TurnID: "voice-turn", Delta: &protocol.Delta{ItemID: "agent-1", Text: "the public answer"}},
		{Type: "item.completed", ThreadID: "thread-worker", TurnID: "voice-turn", Item: &protocol.ThreadItem{ID: "agent-1", Type: "agent_message", Phase: "final_answer", Text: "the public answer"}},
		{Type: "turn.completed", ThreadID: "thread-worker", TurnID: "voice-turn"},
	}
}

// TestBufferedThreadReplayHidesDelegatedVoiceContentLikeRust drives a
// background thread's replay through applyAgentSwitchResult, the path a
// switched-to subagent uses, and checks the rendered transcript: the spoken
// prompt replaces the delegation envelope and the delegated turn's private
// commentary never reaches the view.
func TestBufferedThreadReplayHidesDelegatedVoiceContentLikeRust(t *testing.T) {
	model := newRealtimeDelegationTestModel(t)
	model.backgroundThreadEvents["thread-worker"] = delegatedReplayEvents()
	model.applyAgentSwitchResult(AgentSwitchResultMsg{
		ThreadID: "thread-worker",
		Response: AgentThreadSwitchResponse{
			Entry:  codextui.AgentThreadEntry{ThreadID: "thread-worker"},
			Status: "idle",
		},
	})

	view := utils.StripANSI(model.viewString())
	if !strings.Contains(view, "the public answer") {
		t.Fatalf("final answer missing after the replay:\n%s", view)
	}
	if strings.Contains(view, "private commentary") {
		t.Fatalf("delegated commentary leaked into the replayed transcript:\n%s", view)
	}
	if strings.Contains(view, "private reasoning") {
		t.Fatalf("delegated reasoning leaked into the replayed transcript:\n%s", view)
	}
	if strings.Contains(view, "<realtime_delegation>") {
		t.Fatalf("the delegation envelope was rendered instead of the spoken prompt:\n%s", view)
	}
	if !strings.Contains(view, "inspect the workspace") {
		t.Fatalf("the spoken prompt is missing from the replayed transcript:\n%s", view)
	}
}

// TestBufferedThreadReplayKeepsTypedTurnsLikeRust is the replay control case: a
// non-delegated buffered thread replays its commentary and prompt unchanged.
func TestBufferedThreadReplayKeepsTypedTurnsLikeRust(t *testing.T) {
	events := []protocol.ThreadEvent{
		{Type: "turn.started", ThreadID: "thread-worker", TurnID: "typed-turn"},
		{Type: "item.completed", ThreadID: "thread-worker", TurnID: "typed-turn", Item: &protocol.ThreadItem{ID: "user-1", Type: "user_message", Text: "typed question"}},
		{Type: "item.delta", ThreadID: "thread-worker", TurnID: "typed-turn", Delta: &protocol.Delta{ItemID: "commentary-1", Text: "typed commentary"}},
		{Type: "item.completed", ThreadID: "thread-worker", TurnID: "typed-turn", Item: &protocol.ThreadItem{ID: "commentary-1", Type: "agent_message", Phase: "commentary", Text: "typed commentary"}},
	}
	messages := applyBufferedThreadReplay(nil, events)
	var joined strings.Builder
	for _, message := range messages {
		joined.WriteString(message.Text)
		joined.WriteString("\n")
	}
	if !strings.Contains(joined.String(), "typed commentary") || !strings.Contains(joined.String(), "typed question") {
		t.Fatalf("typed replay changed: %q", joined.String())
	}
}
