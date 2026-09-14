package app

import (
	"strings"
	"testing"
	"time"

	"codex_go/appserver"
	codextui "codex_go/tui"
)

// TestRemoteTUIThreadMessagesRestoreCompletionMetadataLikeRust covers #43558:
// a resumed thread restores each completed turn's saved completion metadata
// after its items, while failed and interrupted turns get no success footer.
func TestRemoteTUIThreadMessagesRestoreCompletionMetadataLikeRust(t *testing.T) {
	completedAt := time.Date(2026, 9, 12, 14, 32, 0, 0, time.Local).Unix()
	durationMS := int64(125_000)
	thread := &appserver.Thread{Turns: []appserver.Turn{
		{
			ID:          "turn-completed",
			Status:      appserver.TurnStatusCompleted,
			CompletedAt: &completedAt,
			DurationMS:  &durationMS,
			Items: []appserver.ThreadItem{
				{ID: "m1", Type: "agentMessage", Text: "finished"},
			},
		},
		{
			ID:          "turn-failed",
			Status:      appserver.TurnStatusFailed,
			CompletedAt: &completedAt,
			DurationMS:  &durationMS,
			Error:       &appserver.TurnError{Message: "boom"},
			Items:       []appserver.ThreadItem{{ID: "m2", Type: "agentMessage", Text: "oops"}},
		},
		{
			ID:          "turn-interrupted",
			Status:      appserver.TurnStatusInterrupted,
			CompletedAt: &completedAt,
			DurationMS:  &durationMS,
		},
	}}

	messages := remoteTUIThreadMessagesFromThread(thread, reasoningProjectionChatWidget, false)
	footers := 0
	for _, message := range messages {
		if message.Role == codextui.RoleHistory && strings.Contains(message.Text, "done ") {
			footers++
			if !strings.Contains(message.Text, "Worked for 2m 5s") {
				t.Fatalf("restored footer missing duration: %q", message.Text)
			}
		}
	}
	if footers != 1 {
		t.Fatalf("completion footers = %d, want exactly one (completed turn only); messages=%#v", footers, messages)
	}

	// A completed turn without saved metadata renders nothing (replay never
	// falls back to the local clock).
	bare := remoteTUIThreadMessagesFromThread(&appserver.Thread{Turns: []appserver.Turn{{
		ID:     "turn-completed-bare",
		Status: appserver.TurnStatusCompleted,
		Items:  []appserver.ThreadItem{{ID: "m3", Type: "agentMessage", Text: "done"}},
	}}}, reasoningProjectionChatWidget, false)
	for _, message := range bare {
		if strings.Contains(message.Text, "done ") {
			t.Fatalf("bare completed turn restored a footer: %#v", bare)
		}
	}
}
