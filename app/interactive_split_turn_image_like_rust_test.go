package app

import (
	"path/filepath"
	"strings"
	"testing"

	"codex_go/session"
)

// TestInteractiveSplitTurnKeepsGeneratedImageWithItsAnswerLikeRust covers Rust
// #61919 history_pagination::split_image_insertion: a page that only begins a
// turn reports that turn's completed, saved image separately so the reader can
// place it under the answer instead of at the top of the page.
func TestInteractiveSplitTurnKeepsGeneratedImageWithItsAnswerLikeRust(t *testing.T) {
	threadID := session.ThreadID("123e4567-e89b-12d3-a456-426614174231")
	prompt := interruptionHistoryItem(0, "item-prompt", "turn-image", "user", "draw a cat")
	image := interruptionHistoryItem(1, "item-image", "turn-image", "assistant", "Generated Image")
	image.Type = "imageGeneration"
	image.Status = "completed"
	image.Data = map[string]any{"savedPath": "/tmp/generated-cat.png"}
	answer := interruptionHistoryItem(2, "item-answer", "turn-image", "assistant", "Here is the image.")
	next := interruptionHistoryItem(3, "item-next", "turn-next", "user", "another question")
	items := []session.Item{prompt, image, answer, next}
	turns := []session.TurnSnapshot{
		{ID: "turn-image", Status: "completed"},
		{ID: "turn-next", Status: "completed"},
	}
	store := saveInterruptionHistoryRecord(t, threadID, turns, items)

	messages, splitTurnID, splitImages := interactivePageMessagesSplit(store, threadID, items[:2], "")
	if splitTurnID != "turn-image" {
		t.Fatalf("split turn = %q, want turn-image", splitTurnID)
	}
	if len(splitImages) != 1 || !splitImages[0].ImageGenerated ||
		!strings.Contains(filepath.ToSlash(splitImages[0].ImagePath), "generated-cat.png") {
		t.Fatalf("split images = %#v", splitImages)
	}
	if len(messages) != 1 || !strings.Contains(messages[0].Text, "draw a cat") {
		t.Fatalf("page rows = %#v", messages)
	}

	// A page that holds the turn's last item keeps every row on the page.
	messages, splitTurnID, splitImages = interactivePageMessagesSplit(store, threadID, items[:3], "")
	if splitTurnID != "" || len(splitImages) != 0 {
		t.Fatalf("completed turn split = %q images=%d, want none", splitTurnID, len(splitImages))
	}
	if len(messages) != 3 {
		t.Fatalf("completed page rows = %d, want 3", len(messages))
	}
}
