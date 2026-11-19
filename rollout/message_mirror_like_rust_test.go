package rollout

import (
	"testing"
	"time"
)

// Rust's ThreadHistoryBuilder only builds transcript items from the event
// stream: a `response_item` message is replayed through ContextManager and
// ignored by `handle_response_item` unless it is a hook prompt
// (app-server-protocol/src/protocol/thread_history.rs). Go keeps both halves in
// one record list, so the compatibility event must fold into the recorded
// response item instead of producing a second transcript bubble.
func TestLegacyUserMessageEventMirrorsResponseItemLikeRust(t *testing.T) {
	lines := []Line{
		responseItemLine(t, map[string]any{
			"type": "message", "role": "user",
			"content": []any{map[string]any{"type": "input_text", "text": "hello world"}},
		}),
		mustEventLine(t, map[string]any{"type": "user_message", "message": "hello world"}),
	}
	items, _ := sessionItemsFromRolloutLines(lines, time.Unix(0, 0).UTC())
	if len(items) != 1 {
		t.Fatalf("items = %#v, want the response item and the event folded together", items)
	}
	if items[0].Role != "user" || items[0].Text != "hello world" {
		t.Fatalf("item = %#v, want the recorded user message", items[0])
	}
	if len(items[0].Raw) == 0 {
		t.Fatalf("item = %#v, want the recorded response payload kept", items[0])
	}
}

func TestLegacyAgentMessageEventMirrorsResponseItemLikeRust(t *testing.T) {
	lines := []Line{
		responseItemLine(t, map[string]any{
			"type": "message", "role": "assistant",
			"content": []any{map[string]any{"type": "output_text", "text": "hi there"}},
		}),
		mustEventLine(t, map[string]any{"type": "agent_message", "message": "hi there"}),
	}
	items, _ := sessionItemsFromRolloutLines(lines, time.Unix(0, 0).UTC())
	if len(items) != 1 {
		t.Fatalf("items = %#v, want one assistant message", items)
	}
	if items[0].Role != "assistant" || items[0].Text != "hi there" {
		t.Fatalf("item = %#v, want the recorded agent message", items[0])
	}
}

// A legacy rollout whose response item was dropped still has to show the
// compatibility event; folding must not swallow real messages.
func TestLegacyUserMessageEventWithoutResponseItemLikeRust(t *testing.T) {
	lines := []Line{
		mustEventLine(t, map[string]any{"type": "user_message", "message": "only event"}),
		mustEventLine(t, map[string]any{"type": "user_message", "message": "only event"}),
	}
	items, _ := sessionItemsFromRolloutLines(lines, time.Unix(0, 0).UTC())
	if len(items) != 2 {
		t.Fatalf("items = %#v, want both repeated events kept", items)
	}
}
