package tea

import (
	"strings"
	"testing"

	"codex_go/tui/history_cell"
)

// TestUnifiedExecPreviewKeepsFullTranscriptLikeRust pins the #42107 behavior:
// inline, transcript, and raw history all retain the complete payload.
func TestUnifiedExecPreviewKeepsFullTranscriptLikeRust(t *testing.T) {
	input := strings.Repeat("line\n", 40) + "tail-sentinel"
	cell := historycell.NewUnifiedExecInteraction("cat", input)
	message := historyMessageFromCell(cell, 80)
	if !strings.Contains(message.Text, "tail-sentinel") {
		t.Fatal("inline history lost the complete input")
	}
	if !strings.Contains(message.TranscriptText, "tail-sentinel") {
		t.Fatal("transcript text lost the complete input")
	}
	if !strings.Contains(message.RawText, "tail-sentinel") {
		t.Fatal("raw text lost the complete input")
	}
}
