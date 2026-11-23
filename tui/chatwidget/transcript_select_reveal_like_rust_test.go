package chatwidget

import (
	"testing"
)

// Rust #63056: the transcript selection caret stays inside the viewport when
// the pane shrinks or the pinned prompt header reserves a row.
func TestSelectionCursorStaysVisibleAfterResizeLikeRust(t *testing.T) {
	overlay := NewTranscriptOverlay(40, 14, numberedTranscript(60))
	overlay.BeginSelection()
	overlay.HandleSelectCommand(SelectCmdEnd)
	row, _ := overlay.SelectionCursor()
	offset := overlay.viewport.YOffset()
	height := overlay.bodyHeight()
	if row < offset || row >= offset+height {
		t.Fatalf("caret row %d outside viewport [%d,%d)", row, offset, offset+height)
	}

	overlay.Resize(40, 8)
	height = overlay.bodyHeight()
	offset = overlay.viewport.YOffset()
	if row < offset || row >= offset+height {
		t.Fatalf("after shrink caret row %d outside viewport [%d,%d)", row, offset, offset+height)
	}
	if row != offset+height-1 {
		t.Fatalf("shrink scrolled to offset %d, want the caret on the last body row", offset)
	}

	overlay.SetPinnedHeader("prompt")
	height = overlay.bodyHeight()
	offset = overlay.viewport.YOffset()
	if row < offset || row >= offset+height {
		t.Fatalf("after pinned header caret row %d outside viewport [%d,%d)", row, offset, offset+height)
	}
}

func TestSelectionCursorRevealsWhenMovingPastTheBottomLikeRust(t *testing.T) {
	overlay := NewTranscriptOverlay(40, 10, numberedTranscript(40))
	overlay.BeginSelection()
	overlay.HandleSelectCommand(SelectCmdBeginning)
	for range 40 {
		overlay.HandleSelectCommand(SelectCmdDown)
		row, _ := overlay.SelectionCursor()
		height := overlay.bodyHeight()
		offset := overlay.viewport.YOffset()
		if row < offset || row >= offset+height {
			t.Fatalf("caret row %d outside viewport [%d,%d)", row, offset, offset+height)
		}
	}
}
