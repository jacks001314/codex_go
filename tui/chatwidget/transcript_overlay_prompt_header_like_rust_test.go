package chatwidget

import (
	"strings"
	"testing"
)

// TestTranscriptOverlayPinnedHeaderReservesOneRowLikeRust covers Rust
// transcript_view::prompt_header: the header is chrome above the content, so
// the body loses one row while the content and reading position stay put.
func TestTranscriptOverlayPinnedHeaderReservesOneRowLikeRust(t *testing.T) {
	overlay := NewTranscriptOverlay(40, 10, numberedTranscript(30))
	overlay.ApplyPagerAction(PagerJumpTop)
	content := overlay.Content()
	offset := overlay.YOffset()
	firstLine := strings.Split(content, "\n")[0]

	overlay.SetPinnedHeader("› question-1")
	if got := overlay.PinnedHeader(); got != "› question-1" {
		t.Fatalf("pinned header = %q", got)
	}
	if overlay.Content() != content {
		t.Fatal("pinned header joined the transcript content")
	}
	if overlay.YOffset() != offset {
		t.Fatalf("offset = %d, want held at %d", overlay.YOffset(), offset)
	}
	rows := strings.Split(overlay.View(), "\n")
	if len(rows) != 10 {
		t.Fatalf("overlay rows = %d, want 10", len(rows))
	}
	if !strings.Contains(rows[1], "question-1") {
		t.Fatalf("row under the title = %q, want the pinned header", rows[1])
	}
	if strings.TrimRight(rows[2], " ") != strings.TrimRight(firstLine, " ") {
		t.Fatalf("first content row = %q, want %q", rows[2], firstLine)
	}

	overlay.SetPinnedHeader("")
	if got := overlay.PinnedHeader(); got != "" {
		t.Fatalf("pinned header = %q, want cleared", got)
	}
	if rows := strings.Split(overlay.View(), "\n"); len(rows) != 10 || strings.TrimRight(rows[1], " ") != strings.TrimRight(firstLine, " ") {
		t.Fatalf("cleared header did not return the row to the content: %q", rows[1])
	}
}

// TestTranscriptOverlayPinnedHeaderKeepsFollowingTailLikeRust keeps the pager
// pinned to the newest row when the header appears or disappears at the tail.
func TestTranscriptOverlayPinnedHeaderKeepsFollowingTailLikeRust(t *testing.T) {
	overlay := NewTranscriptOverlay(40, 10, numberedTranscript(30))
	if !overlay.AtBottom() {
		t.Fatal("overlay did not start at the tail")
	}
	overlay.SetPinnedHeader("› question-1")
	if !overlay.AtBottom() {
		t.Fatalf("pinned header unpinned the tail; offset=%d", overlay.YOffset())
	}
	overlay.SetContent(numberedTranscript(40))
	if !overlay.AtBottom() {
		t.Fatalf("pinned header stopped the pager following appended content")
	}
}
