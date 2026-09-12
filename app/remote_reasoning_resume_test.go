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

	messages := remoteTUIThreadMessagesFromThread(activeReasoningThreadFixture(appserver.TurnStatusInProgress))
	for _, message := range messages {
		if strings.Contains(message.Text, "Step one") {
			t.Fatalf("provisional reasoning leaked into the transcript: %#v", messages)
		}
	}

	// A completed turn keeps its reasoning in the transcript.
	completed := remoteTUIThreadMessagesFromThread(activeReasoningThreadFixture(appserver.TurnStatusCompleted))
	found := false
	for _, message := range completed {
		if strings.Contains(message.Text, "Step one") {
			found = true
		}
	}
	if !found {
		t.Fatalf("completed reasoning missing from the transcript: %#v", completed)
	}
	if _, _, _, ok := remoteTUIThreadActiveReasoning(activeReasoningThreadFixture(appserver.TurnStatusCompleted)); ok {
		t.Fatal("completed turn reported an active reasoning item")
	}
}
