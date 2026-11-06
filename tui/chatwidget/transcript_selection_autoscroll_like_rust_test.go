package chatwidget

import (
	"fmt"
	"strings"
	"testing"

	mousemsg "codex_go/tui/mousemsg"
)

func autoscrollOverlay(t *testing.T) *TranscriptOverlay {
	t.Helper()
	lines := make([]string, 40)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %02d of the transcript", i)
	}
	overlay := NewTranscriptOverlay(40, 8, strings.Join(lines, "\n"))
	overlay.viewport.SetYOffset(5)
	return overlay
}

// Rust #47137: a horizontal drag along an edge row must not autoscroll, so the
// text under the pointer stays put while it is being selected.
func TestTranscriptHorizontalEdgeDragDoesNotAutoscrollLikeRust(t *testing.T) {
	overlay := autoscrollOverlay(t)
	before := overlay.viewport.YOffset()
	sendTranscriptMouse(overlay, mousemsg.MouseActionPress, 2, 1, false)
	sendTranscriptMouse(overlay, mousemsg.MouseActionMotion, 9, 1, false)
	sendTranscriptMouse(overlay, mousemsg.MouseActionMotion, 17, 1, false)
	if got := overlay.viewport.YOffset(); got != before {
		t.Fatalf("horizontal edge drag scrolled from %d to %d", before, got)
	}
}

// A vertical drag that reaches the top row scrolls one line per motion event.
func TestTranscriptVerticalDragAutoscrollsLikeRust(t *testing.T) {
	overlay := autoscrollOverlay(t)
	before := overlay.viewport.YOffset()
	sendTranscriptMouse(overlay, mousemsg.MouseActionPress, 2, 4, false)
	sendTranscriptMouse(overlay, mousemsg.MouseActionMotion, 2, 1, false)
	if got := overlay.viewport.YOffset(); got != before-1 {
		t.Fatalf("top-edge autoscroll = %d, want %d", got, before-1)
	}
	sendTranscriptMouse(overlay, mousemsg.MouseActionMotion, 2, 1, false)
	if got := overlay.viewport.YOffset(); got != before-2 {
		t.Fatalf("repeated top-edge autoscroll = %d, want %d", got, before-2)
	}
}

// The vertical-movement flag is sticky: returning to the starting edge row
// after a vertical drag still autoscrolls.
func TestTranscriptAutoscrollSurvivesReturnToOriginRowLikeRust(t *testing.T) {
	overlay := autoscrollOverlay(t)
	before := overlay.viewport.YOffset()
	sendTranscriptMouse(overlay, mousemsg.MouseActionPress, 2, 1, false)
	sendTranscriptMouse(overlay, mousemsg.MouseActionMotion, 2, 4, false)
	if got := overlay.viewport.YOffset(); got != before {
		t.Fatalf("drag away from the edge scrolled to %d, want %d", got, before)
	}
	sendTranscriptMouse(overlay, mousemsg.MouseActionMotion, 2, 1, false)
	if got := overlay.viewport.YOffset(); got != before-1 {
		t.Fatalf("return to origin row = %d, want %d", got, before-1)
	}
}

// The bottom body row scrolls toward newer content.
func TestTranscriptBottomEdgeDragAutoscrollsLikeRust(t *testing.T) {
	overlay := autoscrollOverlay(t)
	before := overlay.viewport.YOffset()
	sendTranscriptMouse(overlay, mousemsg.MouseActionPress, 2, 4, false)
	sendTranscriptMouse(overlay, mousemsg.MouseActionMotion, 2, 6, false)
	sendTranscriptMouse(overlay, mousemsg.MouseActionMotion, 2, 7, false)
	if got := overlay.viewport.YOffset(); got != before+1 {
		t.Fatalf("bottom-edge autoscroll = %d, want %d", got, before+1)
	}
}
