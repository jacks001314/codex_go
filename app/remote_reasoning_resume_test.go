package app

import (
	"strings"
	"testing"

	"codex_go/appserver"
)

func activeReasoningThreadFixture(status appserver.TurnStatus) *appserver.Thread {
	return &appserver.Thread{
		Turns: []appserver.Turn{{
			ID:     "turn-1",
			Status: status,
			Items: []appserver.ThreadItem{
				{ID: "user-1", Type: "userMessage", Text: "hi"},
				{ID: "reasoning-1", Type: "reasoning", Data: map[string]any{"summary": []any{"## Step one"}}},
			},
		}},
	}
}

// TestRemoteTUIThreadActiveReasoning covers Rust #43921's resume restore: the
// trailing reasoning item of an in-progress turn is the live status heading,
// not a committed transcript entry.
func TestRemoteTUIThreadActiveReasoning(t *testing.T) {
	turnID, itemID, heading, ok := remoteTUIThreadActiveReasoning(activeReasoningThreadFixture(appserver.TurnStatusInProgress))
	if !ok || turnID != "turn-1" || itemID != "reasoning-1" || heading != "Step one" {
		t.Fatalf("active reasoning = %q/%q/%q/%v", turnID, itemID, heading, ok)
	}

	// The reasoning item is restored as the transcript-only block Rust's
	// new_reasoning_summary_block records, carrying its item id so the live
	// completion replaces the restored snapshot instead of duplicating it.
	assertTranscriptOnlyReasoning := func(status appserver.TurnStatus) {
		t.Helper()
		messages := remoteTUIThreadMessagesFromThread(activeReasoningThreadFixture(status), reasoningProjectionChatWidget, false)
		found := false
		for _, message := range messages {
			if !strings.Contains(message.Text, "Step one") {
				continue
			}
			if !message.TranscriptOnly || message.ItemID != "reasoning-1" {
				t.Fatalf("reasoning entry is not transcript-only: %#v", message)
			}
			found = true
		}
		if !found {
			t.Fatalf("reasoning block missing from the transcript: %#v", messages)
		}
	}
	assertTranscriptOnlyReasoning(appserver.TurnStatusInProgress)
	assertTranscriptOnlyReasoning(appserver.TurnStatusCompleted)
	if _, _, _, ok := remoteTUIThreadActiveReasoning(activeReasoningThreadFixture(appserver.TurnStatusCompleted)); ok {
		t.Fatal("completed turn reported an active reasoning item")
	}
}
