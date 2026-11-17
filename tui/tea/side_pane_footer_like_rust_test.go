package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
)

// Rust #61141 lays the inactive pane out as a fixed frame: the windowed
// transcript, a blank row, the inactive draft preview, the dimmed retained
// status, and a blank hint row. The frame always fills the pane height.
func TestSidePaneDraftPreviewFrameLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 40})
	model.paneStatusText = map[string]string{"thread-side": "model gpt-5"}

	frame := sidePaneFrameFor(12)
	if frame.transcript < 1 || frame.status != 1 || frame.composer != 3 || frame.hint != 1 {
		t.Fatalf("frame = %#v", frame)
	}

	rows, _, _ := model.composeSidePane("thread-side", "hello\nthere", "model gpt-5", "!echo draft", 40, 12, sidePaneTail)
	if len(rows) != 12 {
		t.Fatalf("rows = %d, want 12: %#v", len(rows), rows)
	}
	if rows[len(rows)-1] != "" {
		t.Fatalf("hint row is not blank: %q", rows[len(rows)-1])
	}
	draftRow, statusRow := -1, -1
	for index, row := range rows {
		if strings.Contains(row, "!echo draft") {
			draftRow = index
			if !strings.Contains(row, "\u203a !echo draft") {
				t.Fatalf("draft row missing prompt prefix: %q", row)
			}
			if !strings.HasPrefix(row, "\x1b[2m") || !strings.HasSuffix(row, "\x1b[0m") {
				t.Fatalf("draft row is not dimmed: %q", row)
			}
		}
		if strings.Contains(row, "model gpt-5") {
			statusRow = index
		}
	}
	if statusRow < 0 || draftRow < 0 {
		t.Fatalf("missing status (%d) or draft (%d): %#v", statusRow, draftRow, rows)
	}
	if statusRow < draftRow {
		t.Fatalf("status row %d sits above draft row %d", statusRow, draftRow)
	}
	if draftRow >= len(rows)-1 {
		t.Fatalf("draft row %d leaves no hint row: %#v", draftRow, rows)
	}

	joined := model.zipSidePanes(rows, rows, 20, 12)
	if got := len(strings.Split(joined, "\n")); got != 12 {
		t.Fatalf("joined rows = %d, want 12", got)
	}
}

// Only the conversation that lost focus previews its saved draft; the pane
// that owns the composer keeps the live one.
func TestSidePaneDraftPreviewsOnlyUnfocusedSideLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 40})
	model.activeSide = &activeSideConversation{
		ParentThreadID: "thread-parent",
		SideThreadID:   "thread-side",
		ParentDraft:    "parent draft",
		SideDraft:      "side draft",
	}

	parentRows, _, _ := model.renderPassiveSidePane(model.activeSide, false, 40, 12)
	parentPane := strings.Join(parentRows, "\n")
	if !strings.Contains(parentPane, "parent draft") || strings.Contains(parentPane, "side draft") {
		t.Fatalf("passive parent pane drafts = %q", parentPane)
	}
	sideRows, _, _ := model.renderPassiveSidePane(model.activeSide, true, 40, 12)
	sidePane := strings.Join(sideRows, "\n")
	if !strings.Contains(sidePane, "side draft") || strings.Contains(sidePane, "parent draft") {
		t.Fatalf("passive side pane drafts = %q", sidePane)
	}
}
