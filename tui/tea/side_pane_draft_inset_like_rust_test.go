package tea

import (
	"strings"
	"testing"
)

// Rust side_pane::paint_companion gives a multi-row composer a one-row inset:
// the parked draft preview starts on the composer's second row, one row below
// the separator above it. A single-row composer has no inset.
func TestSidePaneParkedDraftInsetsLikeRust(t *testing.T) {
	model := &Model{}
	height := 17
	frame := sidePaneFrameFor(height)
	if frame.composer < 2 {
		t.Fatalf("composer rows = %d, want at least 2 for this fixture", frame.composer)
	}
	rows, _, _ := model.composeSidePane("thread-side", "history", "", "draft", 40, height, sidePaneTail)
	if len(rows) != height {
		t.Fatalf("rows = %d, want %d", len(rows), height)
	}
	separator := rows[frame.transcript]
	if strings.TrimSpace(separator) != "" {
		t.Fatalf("separator row = %q, want blank", separator)
	}
	inset := rows[frame.transcript+1]
	if strings.TrimSpace(inset) != "" {
		t.Fatalf("composer inset row = %q, want blank", inset)
	}
	draft := rows[frame.transcript+2]
	if !strings.Contains(draft, "draft") {
		t.Fatalf("draft row = %q, want the preview one row below the inset", draft)
	}

	single := sidePaneFrameFor(5)
	if single.composer != 1 {
		t.Fatalf("composer rows = %d, want 1 at this height", single.composer)
	}
	short, _, _ := model.composeSidePane("thread-side", "", "", "draft", 40, 5, sidePaneTail)
	if got := short[single.transcript+1]; !strings.Contains(got, "draft") {
		t.Fatalf("single-row composer draft = %q, want the preview directly below the separator", got)
	}
}
