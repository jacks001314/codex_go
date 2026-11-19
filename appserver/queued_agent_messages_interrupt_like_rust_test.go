package appserver

import (
	"testing"
	"time"

	"codex_go/turn"
)

// TestPreserveQueuedAgentMessagesOnInterruptLikeRust mirrors Rust #62348: an
// acknowledged child final that is still queued when the parent turn is
// interrupted is recorded in history, while non-mail input stays queued for the
// normal teardown.
func TestPreserveQueuedAgentMessagesOnInterruptLikeRust(t *testing.T) {
	router, _ := newAgentMessageWakeTestRouter(t, false)
	mail := runtimeAgentCommunicationInputItem("/root/worker", "/root", "The worker finished.", false, true)
	user := map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "hello"}}}
	mailbox := router.requireSteerMailbox()
	if err := mailbox.Enqueue(&turn.SteerEnqueueParams{ThreadID: "root", TurnID: "turn-1", InputItems: []any{mail, user}}); err != nil {
		t.Fatal(err)
	}

	router.preserveQueuedAgentMessagesOnInterrupt("root", "turn-1", time.Now().UTC())

	remaining := mailbox.Drain(&turn.SteerDrainParams{ThreadID: "root", TurnID: "turn-1"})
	if len(remaining) != 1 || remaining[0].(map[string]any)["type"] != "message" {
		t.Fatalf("remaining input = %#v, want only the user message", remaining)
	}
	record, err := router.threadRecord("root", false, true)
	if err != nil || record == nil {
		t.Fatalf("thread record = %#v, %v", record, err)
	}
	if len(record.Items) != 1 || record.Items[0].Type != "agent_message" {
		t.Fatalf("recorded items = %#v, want the preserved agent message", record.Items)
	}
	metadata, _ := record.Items[0].Data[harnessMetadataKey].(map[string]any)
	source, _ := metadata["retained_source"].(map[string]any)
	identity, _ := source["id"].(map[string]any)
	if id, _ := identity["message_id"].(string); id == "" {
		t.Fatalf("preserved agent message has no identity: %#v", record.Items[0].Data)
	}
}
