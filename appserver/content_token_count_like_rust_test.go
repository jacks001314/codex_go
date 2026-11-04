package appserver

import (
	"encoding/json"
	"testing"

	"codex_go/model"
	"codex_go/turn"
)

func contentTokenCountPtr(value uint64) *uint64 { return &value }

// Rust #59041: item/completed carries the producer-supplied count, and the
// field stays present as null when it is unavailable.
func TestItemCompletedNotificationReportsContentTokenCountLikeRust(t *testing.T) {
	withCount, err := json.Marshal(&ItemCompletedNotification{
		Item:     ThreadItemPayload{"id": "msg-1", "type": "agentMessage", "text": "done"},
		ThreadID: "thread-1", TurnID: "turn-1", ContentTokenCount: contentTokenCountPtr(12), CompletedAtMS: 99,
	})
	if err != nil {
		t.Fatalf("marshal with count: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(withCount, &decoded); err != nil {
		t.Fatalf("unmarshal with count: %v", err)
	}
	if got, ok := decoded["contentTokenCount"].(float64); !ok || got != 12 {
		t.Fatalf("contentTokenCount = %#v, want 12 (%s)", decoded["contentTokenCount"], withCount)
	}

	withoutCount, err := json.Marshal(&ItemCompletedNotification{Item: ThreadItemPayload{"id": "tool-1", "type": "commandExecution"}})
	if err != nil {
		t.Fatalf("marshal without count: %v", err)
	}
	decoded = map[string]any{}
	if err := json.Unmarshal(withoutCount, &decoded); err != nil {
		t.Fatalf("unmarshal without count: %v", err)
	}
	value, present := decoded["contentTokenCount"]
	if !present || value != nil {
		t.Fatalf("contentTokenCount = %#v (present=%v), want present null (%s)", value, present, withoutCount)
	}
}

func TestContentTokenCountsByItemIDOnlyCoversAssistantMessages(t *testing.T) {
	result := &turn.AgentLoopResult{Responses: []*model.AgentResponse{{
		Items: []model.AgentItem{
			{ID: "msg-1", Type: "agent_message", Text: "done", ContentTokenCount: contentTokenCountPtr(0)},
			{ID: "msg-2", Type: "message", Text: "plan", ContentTokenCount: contentTokenCountPtr(9)},
			{ID: "tool-1", Type: "function_call", Name: "exec", ContentTokenCount: contentTokenCountPtr(5)},
			{ID: "", Type: "agent_message", Text: "anon", ContentTokenCount: contentTokenCountPtr(4)},
		},
	}}}
	counts := contentTokenCountsByItemID(result)
	if len(counts) != 2 {
		t.Fatalf("counts = %#v, want two assistant messages", counts)
	}
	if got := counts["msg-1"]; got == nil || *got != 0 {
		t.Fatalf("msg-1 count = %v, want 0", got)
	}
	if got := counts["msg-2"]; got == nil || *got != 9 {
		t.Fatalf("msg-2 count = %v, want 9", got)
	}
	if _, ok := counts["tool-1"]; ok {
		t.Fatalf("tool call carried a content token count: %#v", counts)
	}
	if counts := contentTokenCountsByItemID(nil); len(counts) != 0 {
		t.Fatalf("nil result counts = %#v", counts)
	}
}
