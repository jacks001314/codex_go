package tea

import (
	"strings"
	"testing"

	"codex_go/protocol"
	codextui "codex_go/tui"
)

func startStreamedAnswer(t *testing.T, model *Model, text string) {
	t.Helper()
	model.Update(ThreadEventMsg{Event: protocol.ThreadEvent{
		Type:  "item.delta",
		Delta: &protocol.Delta{ItemID: "answer-1", Text: text},
	}})
}

func startSubAgentActivity(model *Model) {
	model.Update(ThreadEventMsg{Event: protocol.ThreadEvent{
		Type: "item.started",
		Item: &protocol.ThreadItem{
			ID:           "subagent-1",
			Type:         "sub_agent_activity",
			ActivityKind: "started",
			AgentPath:    "/root/worker",
		},
	}})
}

func assertAnswerBeforeActivity(t *testing.T, model *Model, answer string) {
	t.Helper()
	answerIndex, activityIndex := -1, -1
	for index, message := range model.State.Messages {
		if message.Role == codextui.RoleAssistant && strings.Contains(message.Text, answer) {
			answerIndex = index
		}
		if message.Role == codextui.RoleHistory && strings.Contains(message.Text, "/root/worker") {
			activityIndex = index
		}
	}
	if answerIndex < 0 || activityIndex < 0 || answerIndex > activityIndex {
		t.Fatalf("answer index=%d activity index=%d messages=%#v", answerIndex, activityIndex, model.State.Messages)
	}
}

// Rust #46867: subagent activity arriving mid-stream waits until the
// authoritative answer settles, preserving one complete answer before activity.
func TestSubAgentActivityWaitsForFinalAnswerLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 30})
	startStreamedAnswer(t, model, "The answer")
	startSubAgentActivity(model)
	if len(model.deferredSubAgentActivity) != 1 {
		t.Fatalf("deferred activity = %#v", model.deferredSubAgentActivity)
	}

	model.Update(TurnCompletedMsg{AssistantMessage: "The answer is complete."})
	if len(model.deferredSubAgentActivity) != 0 {
		t.Fatalf("activity not flushed: %#v", model.deferredSubAgentActivity)
	}
	assertAnswerBeforeActivity(t, model, "The answer is complete.")
}

// Rust #46867: interruption/failure still flush late activity after the answer.
func TestSubAgentActivityFlushesAtInterruptedTurnEndLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 30})
	startStreamedAnswer(t, model, "Partial answer")
	startSubAgentActivity(model)
	model.Update(TurnInterruptedMsg{})
	if len(model.deferredSubAgentActivity) != 0 {
		t.Fatalf("activity not flushed after interruption: %#v", model.deferredSubAgentActivity)
	}
	assertAnswerBeforeActivity(t, model, "Partial answer")
}
