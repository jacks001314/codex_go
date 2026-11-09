package tea

import (
	"testing"

	codextui "codex_go/tui"
)

// Mirrors Rust #60335: replaying a finished Cloud assistant item and then
// receiving its live completion leaves one assistant message, not two.
func TestReplayedAssistantMessageReplacedByLiveCompletionLikeRust(t *testing.T) {
	state := &codextui.State{Messages: []codextui.Message{{
		Role: codextui.RoleAssistant, Text: "already answered", RawText: "already answered", ItemID: "cloud-item-1",
	}}}
	component := &TranscriptComponent{}
	// A late delta can open a provisional second copy for the same item.
	state.Messages = append(state.Messages, codextui.Message{
		Role: codextui.RoleAssistant, Text: "already answered", ItemID: "cloud-item-1",
	})
	component.mergeAssistantFinalForItem(state, "cloud-item-1", "already answered", 80)
	count := 0
	for _, message := range state.Messages {
		if message.Role == codextui.RoleAssistant && message.ItemID == "cloud-item-1" {
			count++
			if message.Text != "already answered" {
				t.Fatalf("assistant text = %q", message.Text)
			}
		}
	}
	if count != 1 {
		t.Fatalf("assistant messages for the replayed item = %d, want 1", count)
	}
}
