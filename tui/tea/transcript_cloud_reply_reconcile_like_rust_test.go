package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
)

// TestCloudDirectiveOnlyCompletionRemovesPrefixLikeRust mirrors Rust #62403:
// when the authoritative completion carries only hidden directives, the
// restored or streamed reply fragments are removed from the transcript instead
// of leaving a stale prefix behind.
func TestCloudDirectiveOnlyCompletionRemovesPrefixLikeRust(t *testing.T) {
	state := &codextui.State{Messages: []codextui.Message{
		{Role: codextui.RoleUser, Text: "hello"},
		{Role: codextui.RoleAssistant, Text: "partial prefix", ItemID: "cloud-item-1"},
		{Role: codextui.RoleAssistant, Text: "partial prefix again", ItemID: "cloud-item-1"},
		{Role: codextui.RoleHistory, Text: "command output"},
		{Role: codextui.RoleAssistant, Text: "second reply", ItemID: "cloud-item-2"},
	}}
	component := &TranscriptComponent{}
	component.mergeAssistantFinalForItem(state, "cloud-item-1", `::git-commit{cwd="/repo"}`, 80)
	if len(state.Messages) != 3 {
		t.Fatalf("messages = %#v, want the user, tool and second reply only", state.Messages)
	}
	for _, message := range state.Messages {
		if strings.TrimSpace(message.ItemID) == "cloud-item-1" {
			t.Fatalf("hidden completion left a prefix behind: %#v", message)
		}
	}
	if state.Messages[1].Role != codextui.RoleHistory || state.Messages[1].Text != "command output" {
		t.Fatalf("tool position = %#v", state.Messages[1])
	}
	if state.Messages[2].Role != codextui.RoleAssistant || state.Messages[2].Text != "second reply" {
		t.Fatalf("second reply = %#v", state.Messages[2])
	}
}

// TestCloudPendingReplyFilledInPlaceLikeRust mirrors the position half of Rust
// #62403: a reply restored without visible text reserves its transcript slot, so
// the completion fills that slot instead of appending after later output.
func TestCloudPendingReplyFilledInPlaceLikeRust(t *testing.T) {
	state := &codextui.State{Messages: []codextui.Message{
		{Role: codextui.RoleUser, Text: "hello"},
		{Role: codextui.RoleAssistant, ItemID: "cloud-item-1"},
		{Role: codextui.RoleHistory, Text: "command output"},
		{Role: codextui.RoleAssistant, Text: "second reply", ItemID: "cloud-item-2"},
	}}
	component := &TranscriptComponent{}
	component.mergeAssistantFinalForItem(state, "cloud-item-1", "final answer", 80)
	if len(state.Messages) != 4 {
		t.Fatalf("messages = %#v", state.Messages)
	}
	if state.Messages[1].Text != "final answer" || state.Messages[1].ItemID != "cloud-item-1" {
		t.Fatalf("completed reply = %#v, want the reserved position", state.Messages[1])
	}
	if state.Messages[2].Role != codextui.RoleHistory {
		t.Fatalf("tool moved: %#v", state.Messages[2])
	}
}

// TestCloudPendingReplySlotRendersInvisiblyLikeRust pins the reserved slot to a
// zero-row cell, so the restored placeholder never paints over the transcript.
func TestCloudPendingReplySlotRendersInvisiblyLikeRust(t *testing.T) {
	base := &codextui.State{Messages: []codextui.Message{
		{Role: codextui.RoleUser, Text: "hello"},
		{Role: codextui.RoleHistory, Text: "command output"},
	}}
	withSlot := &codextui.State{Messages: []codextui.Message{
		{Role: codextui.RoleUser, Text: "hello"},
		{Role: codextui.RoleAssistant, ItemID: "cloud-item-1"},
		{Role: codextui.RoleHistory, Text: "command output"},
	}}
	var baseCache, slotCache transcriptMessageCache
	want := renderTranscriptWithCache(&baseCache, base, false, 80, "dark", false, "", false)
	got := renderTranscriptWithCache(&slotCache, withSlot, false, 80, "dark", false, "", false)
	if got != want {
		t.Fatalf("reserved slot changed the transcript:\nwant:\n%s\ngot:\n%s", want, got)
	}
}

// TestTurnCompletionSkipsReservedCloudSlotLikeRust mirrors Rust #62403's
// completion fallback: the notification reports the latest reply that actually
// completed, not an empty reserved slot.
func TestTurnCompletionSkipsReservedCloudSlotLikeRust(t *testing.T) {
	model := NewModel(&codextui.State{Messages: []codextui.Message{
		{Role: codextui.RoleUser, Text: "hello"},
		{Role: codextui.RoleAssistant, Text: "first answer", ItemID: "cloud-item-1"},
		{Role: codextui.RoleAssistant, ItemID: "cloud-item-2"},
	}}, Options{})
	if got := model.lastAssistantNotificationResponse(); got != "first answer" {
		t.Fatalf("completion fallback = %q, want the completed reply", got)
	}
}
