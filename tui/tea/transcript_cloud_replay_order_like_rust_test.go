package tea

import (
	"testing"

	codextui "codex_go/tui"
)

// TestLateCloudCompletionKeepsStreamLikeRust mirrors Rust #62403: a
// completion for an older reply must not be merged into a reply that is still
// streaming, and it must not become the latest copy source.
func TestLateCloudCompletionKeepsStreamLikeRust(t *testing.T) {
	state := &codextui.State{Messages: []codextui.Message{
		{Role: codextui.RoleAssistant, Text: "older reply", RawText: "older reply", ItemID: "cloud-item-1"},
		{Role: codextui.RoleAssistant, Text: "live partial", ItemID: "cloud-item-2"},
	}}
	component := &TranscriptComponent{activeAssistantDeltaItemID: "cloud-item-2", activeAssistantDeltaTurnID: "turn-1"}
	component.NoteReplayedAssistantMessages(state.Messages)
	component.mergeAssistantFinalForItem(state, "cloud-item-2", "live partial", 80)
	if got := component.LatestCompletedResponse(); got != "live partial" {
		t.Fatalf("latest response = %q, want the streaming reply", got)
	}

	// The older reply loses its recorded message (e.g. a directive-only
	// completion removed it) and completes late.
	state.Messages = state.Messages[1:]
	component.mergeAssistantFinalForItem(state, "cloud-item-1", "older reply final", 80)
	if state.Messages[0].Text != "live partial" || state.Messages[0].ItemID != "cloud-item-2" {
		t.Fatalf("live stream changed: %#v", state.Messages[0])
	}
	if len(state.Messages) != 2 || state.Messages[1].ItemID != "cloud-item-1" || state.Messages[1].Text != "older reply final" {
		t.Fatalf("late completion messages = %#v", state.Messages)
	}
	if got := component.LatestCompletedResponse(); got != "live partial" {
		t.Fatalf("latest response after the late completion = %q, want the newer reply", got)
	}
}

// TestReplayedCloudItemOrderLikeRust mirrors Rust #62403's per-turn map: older
// history pages receive negative positions, and only a finished item suppresses
// its live completion.
func TestReplayedCloudItemOrderLikeRust(t *testing.T) {
	component := &TranscriptComponent{}
	if !component.noteReplayedCloudItem("turn-1", "live-item", false, false) {
		t.Fatal("first observation of a live item was not recorded")
	}
	if component.noteReplayedCloudItem("turn-1", "live-item", true, false) {
		t.Fatal("repeat observation reported a new item")
	}
	if !component.ReplayedCloudItemFinished("turn-1", "live-item") {
		t.Fatal("finished item was not reported as finished")
	}
	if component.ReplayedCloudItemFinished("turn-1", "unknown") {
		t.Fatal("unknown item reported as a replayed finished reply")
	}
	component.noteReplayedCloudItem("turn-1", "old-item", true, true)
	live := component.replayedCloudText["turn-1"]["live-item"].order
	old := component.replayedCloudText["turn-1"]["old-item"].order
	if old >= 0 || old >= live {
		t.Fatalf("older history order = %d, live order = %d, want the older page first", old, live)
	}
}

// TestNotificationFallbackUsesReplyOrderLikeRust mirrors Rust #62403: when a
// turn completion carries no text, the fallback picks the reply with the
// highest observation order even if an older completion was appended later.
func TestNotificationFallbackUsesReplyOrderLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-a")
	model := NewModel(state, Options{Width: 100, Height: 30})
	model.Transcript.NoteReplayedAssistantMessages([]codextui.Message{
		{Role: codextui.RoleAssistant, Text: "older reply", ItemID: "item-1"},
		{Role: codextui.RoleAssistant, Text: "live reply", ItemID: "item-2"},
	})
	model.Transcript.activeAssistantDeltaItemID = "item-2"
	model.State.Messages = []codextui.Message{
		{Role: codextui.RoleAssistant, Text: "older reply", ItemID: "item-1"},
		{Role: codextui.RoleAssistant, Text: "live reply", ItemID: "item-2"},
	}
	model.Transcript.mergeAssistantFinalForItem(model.State, "item-2", "live reply", 80)
	model.State.Messages = model.State.Messages[1:]
	model.Transcript.mergeAssistantFinalForItem(model.State, "item-1", "older reply final", 80)
	if got := model.lastAssistantNotificationResponse(); got != "live reply" {
		t.Fatalf("notification fallback = %q, want the higher-order reply", got)
	}
}
