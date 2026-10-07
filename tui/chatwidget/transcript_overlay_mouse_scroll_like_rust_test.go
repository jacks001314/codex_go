package chatwidget

import (
	"fmt"
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"
)

// Rust #50209 (4dd51f4a5f, "Make transcript mouse scroll speed configurable"):
// the transcript pager applies `tui.mouse_scroll_speed` to each wheel event and
// accumulates fractional movement across events
// (`TranscriptView::handle_mouse` + `TranscriptView::pending_mouse_scroll`).
func TestTranscriptOverlayMouseScrollSpeedLikeRust(t *testing.T) {
	rows := make([]string, 0, 40)
	for i := 0; i < 40; i++ {
		rows = append(rows, fmt.Sprintf("row %02d", i))
	}
	content := strings.Join(rows, "\n")

	wheelUp := bubbletea.MouseMsg{Action: bubbletea.MouseActionPress, Button: bubbletea.MouseButtonWheelUp}
	wheelDown := bubbletea.MouseMsg{Action: bubbletea.MouseActionPress, Button: bubbletea.MouseButtonWheelDown}

	// Default: one row per event, down from the previous fixed three rows.
	overlay := NewTranscriptOverlay(40, 6, content)
	before := overlay.YOffset()
	if before <= 0 {
		t.Fatalf("initial overlay offset = %d, want a scrollable bottom", before)
	}
	overlay.Update(wheelUp)
	if got, want := overlay.YOffset(), before-1; got != want {
		t.Fatalf("default wheel offset = %d, want %d", got, want)
	}

	// A fractional multiplier waits for a whole row, and a reversed wheel
	// restarts the accumulation instead of cancelling it.
	overlay.SetMouseScrollSpeed(0.5)
	before = overlay.YOffset()
	overlay.Update(wheelUp)
	if got := overlay.YOffset(); got != before {
		t.Fatalf("first half-row wheel offset = %d, want %d", got, before)
	}
	overlay.Update(wheelUp)
	if got, want := overlay.YOffset(), before-1; got != want {
		t.Fatalf("second half-row wheel offset = %d, want %d", got, want)
	}
	overlay.Update(wheelUp)
	overlay.Update(wheelDown)
	if got, want := overlay.YOffset(), before-1; got != want {
		t.Fatalf("reversed half-row wheel offset = %d, want %d", got, want)
	}
	if got := overlay.MouseScrollSpeed(); got != 0.5 {
		t.Fatalf("overlay mouse scroll speed = %v, want 0.5", got)
	}
}
