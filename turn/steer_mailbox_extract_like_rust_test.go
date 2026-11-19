package turn

import "testing"

func TestSteerMailboxExtractKeepsOtherInputLikeRust(t *testing.T) {
	mailbox := NewSteerMailbox()
	agentMail := map[string]any{"type": "agent_message", "id": "amsg_1"}
	user := map[string]any{"type": "message", "role": "user"}
	if err := mailbox.Enqueue(&SteerEnqueueParams{ThreadID: "thread", TurnID: "turn", InputItems: []any{agentMail, user}}); err != nil {
		t.Fatal(err)
	}
	extracted := mailbox.Extract("thread", "turn", func(item any) bool {
		payload, ok := item.(map[string]any)
		return ok && payload["type"] == "agent_message"
	})
	if len(extracted) != 1 || extracted[0].(map[string]any)["id"] != "amsg_1" {
		t.Fatalf("extracted = %#v", extracted)
	}
	remaining := mailbox.Drain(&SteerDrainParams{ThreadID: "thread", TurnID: "turn"})
	if len(remaining) != 1 || remaining[0].(map[string]any)["type"] != "message" {
		t.Fatalf("remaining = %#v, want the user message", remaining)
	}
}
