package rollout

import (
	"encoding/json"
	"reflect"
	"testing"

	"codex_go/retainedctx"
)

func steerRollbackUserLine(t *testing.T, turnID string, text string) Line {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"type":    "message",
		"role":    "user",
		"content": []any{map[string]any{"type": "input_text", "text": text}},
	})
	if err != nil {
		t.Fatalf("marshal user item: %v", err)
	}
	return Line{Type: "item", TurnID: turnID, Item: raw}
}

func steerRollbackCallLine(t *testing.T, turnID string, callID string) Line {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"type":      "function_call",
		"call_id":   callID,
		"name":      "request_user_input",
		"arguments": "{}",
	})
	if err != nil {
		t.Fatalf("marshal function call item: %v", err)
	}
	return Line{Type: "item", TurnID: turnID, Item: raw}
}

func steerRollbackAnswerLine(t *testing.T, turnID string, callID string, question string, answer string) Line {
	t.Helper()
	payload, err := json.Marshal(retainedctx.RetainedContextEvent{
		Answer: retainedctx.VerifiedAnswer{
			TurnID:    turnID,
			CallID:    callID,
			Questions: []retainedctx.VerifiedQuestionAnswer{{Question: question, Answer: answer}},
		},
	})
	if err != nil {
		t.Fatalf("marshal retained answer: %v", err)
	}
	return Line{Type: "retained_context", Payload: payload}
}

func steerRollbackAnswerIDs(events []retainedctx.RetainedContextEvent) []string {
	ids := make([]string, 0, len(events))
	for _, event := range events {
		ids = append(ids, event.Answer.CallID)
	}
	return ids
}

func steerRollbackPaginatedItemLine(t *testing.T, turnID string, item map[string]any) Line {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"type":      "item_completed",
		"thread_id": "thread-paginated",
		"turn_id":   turnID,
		"item":      item,
	})
	if err != nil {
		t.Fatalf("marshal paginated item event: %v", err)
	}
	return Line{Type: "event_msg", Payload: payload}
}

func steerRollbackPaginatedUserLine(t *testing.T, turnID string, text string) Line {
	return steerRollbackPaginatedItemLine(t, turnID, map[string]any{
		"type": "UserMessage",
		"id":   "user-" + text,
		"content": []any{
			map[string]any{"type": "Text", "text": text},
		},
	})
}

func steerRollbackPaginatedCallLine(t *testing.T, turnID string, callID string) Line {
	return steerRollbackPaginatedItemLine(t, turnID, map[string]any{
		"type":      "DynamicToolCall",
		"id":        callID,
		"tool":      "request_user_input",
		"arguments": map[string]any{},
	})
}

// Mirrors Rust #42298: a steer adds an instruction boundary without adding a
// turn ID. Rolling it back must keep the answer whose source call survives.
func TestRetainedContextEventsPreserveAnswersBeforeRolledBackSteerLikeRust(t *testing.T) {
	const turnID = "shared-turn"
	lines := []Line{
		steerRollbackUserLine(t, turnID, "Check whether to publish."),
		steerRollbackCallLine(t, turnID, "before-steer"),
		steerRollbackAnswerLine(t, turnID, "before-steer", "Publish?", "Only privately."),
		steerRollbackUserLine(t, turnID, "Also inspect the README."),
		steerRollbackCallLine(t, turnID, "after-steer"),
		steerRollbackAnswerLine(t, turnID, "after-steer", "Publish the README?", "Do not publish it."),
		{Type: "event_msg", ThreadRolledBack: &RollbackEvent{NumTurns: 1}},
	}

	events := RetainedContextEvents(lines)
	if got, want := steerRollbackAnswerIDs(events), []string{"before-steer"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("retained answers after steer rollback = %v, want %v", got, want)
	}
}

// A delayed answer is still owned by the call that asked the question, not by
// the newest steer the running turn has already accepted.
func TestRetainedContextEventsKeepDelayedAnswerAtItsSourceBoundaryLikeRust(t *testing.T) {
	const turnID = "shared-turn"
	lines := []Line{
		steerRollbackUserLine(t, turnID, "Check whether to publish."),
		steerRollbackCallLine(t, turnID, "before-steer"),
		steerRollbackUserLine(t, turnID, "Also inspect the README."),
		steerRollbackAnswerLine(t, turnID, "before-steer", "Publish?", "Only privately."),
		steerRollbackCallLine(t, turnID, "after-steer"),
		steerRollbackAnswerLine(t, turnID, "after-steer", "Publish the README?", "Do not publish it."),
		{Type: "event_msg", ThreadRolledBack: &RollbackEvent{NumTurns: 1}},
	}

	events := RetainedContextEvents(lines)
	if got, want := steerRollbackAnswerIDs(events), []string{"before-steer"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("delayed retained answers after steer rollback = %v, want %v", got, want)
	}
}

// Compaction checkpoints snapshot the retained facts, so rollback filtering
// must apply to the snapshot as well as the sparse events after it.
func TestCompactedRetainedContextPreservesAnswersBeforeRolledBackSteerLikeRust(t *testing.T) {
	const turnID = "shared-turn"
	context := &retainedctx.RetainedContext{}
	context.Record(retainedctx.RetainedContextEvent{Answer: retainedctx.VerifiedAnswer{
		TurnID:    turnID,
		CallID:    "before-steer",
		Questions: []retainedctx.VerifiedQuestionAnswer{{Question: "Publish?", Answer: "Only privately."}},
	}})
	context.Record(retainedctx.RetainedContextEvent{Answer: retainedctx.VerifiedAnswer{
		TurnID:    turnID,
		CallID:    "after-steer",
		Questions: []retainedctx.VerifiedQuestionAnswer{{Question: "Publish the README?", Answer: "Do not publish it."}},
	}})
	checkpoint, err := json.Marshal(map[string]any{
		"message":             "compacted",
		"replacement_history": []any{},
		"retained_context":    context,
	})
	if err != nil {
		t.Fatalf("marshal checkpoint: %v", err)
	}
	lines := []Line{
		steerRollbackUserLine(t, turnID, "Check whether to publish."),
		steerRollbackCallLine(t, turnID, "before-steer"),
		steerRollbackUserLine(t, turnID, "Also inspect the README."),
		steerRollbackCallLine(t, turnID, "after-steer"),
		{Type: "compacted", Payload: checkpoint},
		{Type: "event_msg", ThreadRolledBack: &RollbackEvent{NumTurns: 1}},
	}

	restored := CompactedRetainedContext(lines)
	if restored == nil {
		t.Fatal("CompactedRetainedContext() = nil, want the surviving answer")
	}
	answers := restored.VerifiedAnswers()
	if len(answers) != 1 || answers[0].CallID != "before-steer" {
		t.Fatalf("checkpoint answers after steer rollback = %#v, want only before-steer", answers)
	}
}

func TestRetainedContextEventsKeepAllAnswersWithoutRollbackLikeRust(t *testing.T) {
	const turnID = "shared-turn"
	lines := []Line{
		steerRollbackUserLine(t, turnID, "Check whether to publish."),
		steerRollbackCallLine(t, turnID, "before-steer"),
		steerRollbackAnswerLine(t, turnID, "before-steer", "Publish?", "Only privately."),
		steerRollbackUserLine(t, turnID, "Also inspect the README."),
		steerRollbackCallLine(t, turnID, "after-steer"),
		steerRollbackAnswerLine(t, turnID, "after-steer", "Publish the README?", "Do not publish it."),
	}

	events := RetainedContextEvents(lines)
	if got, want := steerRollbackAnswerIDs(events), []string{"before-steer", "after-steer"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("retained answers without rollback = %v, want %v", got, want)
	}
}

// Paginated rollouts persist user turns and tool calls as item_completed
// events rather than response items, so the same source-call ownership must be
// recovered from the canonical ThreadItem shape.
func TestRetainedContextEventsPreserveAnswersAcrossPaginatedSteerRollbackLikeRust(t *testing.T) {
	const turnID = "shared-turn"
	lines := []Line{
		steerRollbackPaginatedUserLine(t, turnID, "Check whether to publish."),
		steerRollbackPaginatedCallLine(t, turnID, "before-steer"),
		steerRollbackAnswerLine(t, turnID, "before-steer", "Publish?", "Only privately."),
		steerRollbackPaginatedUserLine(t, turnID, "Also inspect the README."),
		steerRollbackPaginatedCallLine(t, turnID, "after-steer"),
		steerRollbackAnswerLine(t, turnID, "after-steer", "Publish the README?", "Do not publish it."),
		{Type: "event_msg", ThreadRolledBack: &RollbackEvent{NumTurns: 1}},
	}

	events := RetainedContextEvents(lines)
	if got, want := steerRollbackAnswerIDs(events), []string{"before-steer"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("paginated retained answers after steer rollback = %v, want %v", got, want)
	}
}
