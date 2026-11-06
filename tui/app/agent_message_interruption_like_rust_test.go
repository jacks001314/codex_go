package app

import "testing"

// TestConsolidateInterruptedAgentMessageLikeRust mirrors Rust #59700: a reply
// interrupted by a tool keeps its original position, the tool order is preserved
// and later fragments for the same item are dropped.
func TestConsolidateInterruptedAgentMessageLikeRust(t *testing.T) {
	key := InterruptedItemKey("turn-1", "msg-1")
	cells := []TranscriptCell{
		{Kind: TranscriptCellAgentMessage, Lines: []string{"partial **markdown"}, InterruptedItem: key},
		{Kind: TranscriptCellGeneric, Lines: []string{"tool ran"}},
		{Kind: TranscriptCellAgentMessage, Lines: []string{"** and tail"}, InterruptedItem: key},
		{Kind: TranscriptCellAgentMessage, Lines: []string{"unrelated"}},
	}
	result := ConsolidateInterruptedAgentMessage(cells, key, "partial **markdown** and tail", "/work")
	if !result.Consolidated || result.Start != 0 {
		t.Fatalf("result = %+v, want consolidated at the first fragment", result)
	}
	if len(result.Cells) != 3 {
		t.Fatalf("cells = %#v, want 3 after dropping the duplicate fragment", result.Cells)
	}
	if result.Cells[0].Kind != TranscriptCellAgentMarkdown || result.Cells[0].Source != "partial **markdown** and tail" {
		t.Fatalf("first cell = %#v, want the completed markdown reply", result.Cells[0])
	}
	if result.Cells[1].Kind != TranscriptCellGeneric {
		t.Fatalf("second cell = %#v, want the intervening tool", result.Cells[1])
	}
	if result.Cells[2].Lines[0] != "unrelated" {
		t.Fatalf("third cell = %#v, want the unrelated reply", result.Cells[2])
	}

	missing := ConsolidateInterruptedAgentMessage(cells, InterruptedItemKey("turn-2", "msg-9"), "later", "")
	if missing.Consolidated || len(missing.Cells) != len(cells) {
		t.Fatalf("unknown item result = %+v, want no change", missing)
	}
}
