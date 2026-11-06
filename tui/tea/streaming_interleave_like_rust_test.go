package tea

import (
	"testing"

	"codex_go/protocol"
	codextui "codex_go/tui"
)

// TestModelInterleavedAssistantMessageStaysAtFirstPositionLikeRust mirrors Rust
// #59700: item/started can carry the first text, a tool flushes the provisional
// reply, a resumed fragment arrives afterwards, and the authoritative item
// completion still replaces the reply before the tool instead of appending a
// duplicate.
func TestModelInterleavedAssistantMessageStaysAtFirstPositionLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-1")
	model := NewModel(state, Options{Width: 80, Height: 24})
	model.Update(ThreadEventMsg{Event: protocol.TurnStartedWithID("turn-1")})

	started := protocol.ThreadItem{ID: "message-1", Type: "agent_message", Text: "I'll inspect"}
	model.Update(ThreadEventMsg{Event: protocol.ItemStarted(started)})
	model.Update(ThreadEventMsg{Event: protocol.ItemStarted(started)})
	if len(state.Messages) != 1 || state.Messages[0].Text != "I'll inspect" {
		t.Fatalf("first text was not streamed exactly once: %#v", state.Messages)
	}

	model.Update(ThreadEventMsg{Event: protocol.AgentMessageDelta("message-1", " the **relevant streaming path")})
	model.Update(ThreadEventMsg{Event: protocol.ItemStarted(protocol.ThreadItem{
		ID:      "command-1",
		Type:    "command_execution",
		Command: "echo ok",
	})})
	assistantIndex, toolIndex := -1, -1
	for index, message := range state.Messages {
		if message.Role == codextui.RoleAssistant && message.ItemID == "message-1" {
			assistantIndex = index
			if message.InterruptedItem != "turn-1:message-1" {
				t.Fatalf("interrupted key = %q", message.InterruptedItem)
			}
		}
		if message.Role == codextui.RoleHistory {
			toolIndex = index
		}
	}
	if assistantIndex < 0 || toolIndex < 0 || assistantIndex > toolIndex {
		t.Fatalf("assistant must be flushed before the tool: %#v", state.Messages)
	}

	// A second start and a resumed delta are provisional fragments of the same
	// item. They must not rewrite the reply already shown before the tool.
	model.Update(ThreadEventMsg{Event: protocol.ItemStarted(protocol.ThreadItem{ID: "message-1", Type: "agent_message"})})
	model.Update(ThreadEventMsg{Event: protocol.AgentMessageDelta("message-1", " first")})
	full := "I'll inspect the **relevant streaming path first.**"
	model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(protocol.AgentMessageItem("message-1", full))})

	matches := 0
	assistantIndex = -1
	for index, message := range state.Messages {
		if message.Role == codextui.RoleAssistant && message.ItemID == "message-1" {
			matches++
			assistantIndex = index
			if message.Text != full {
				t.Fatalf("authoritative reply = %q, want %q", message.Text, full)
			}
			if message.InterruptedItem != "" {
				t.Fatalf("interrupted marker not cleared: %#v", message)
			}
		}
	}
	if matches != 1 || assistantIndex > toolIndex {
		t.Fatalf("reply was duplicated or moved after its tool: %#v", state.Messages)
	}
}
