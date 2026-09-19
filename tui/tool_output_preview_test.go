package tui

import (
	"strings"
	"testing"
)

// Mirrors Rust #46492's tool_output_tests: the preview keeps the leading rows
// within the shared budget and reports the hidden logical lines.
func TestToolOutputPreviewShowsLeadingRowsAndHiddenCount(t *testing.T) {
	lines := []string{"one", "two", "three", "four", "five"}
	got := ToolOutputPreviewLines(lines, 40, len(lines))
	want := []string{"one", "two", "three", "+2 lines (" + TranscriptHint + ")"}
	if len(got) != len(want) {
		t.Fatalf("preview = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("preview = %#v, want %#v", got, want)
		}
	}

	// An exact fit reports nothing hidden.
	if got := ToolOutputPreviewLines([]string{"one", "two", "three"}, 40, 3); len(got) != 3 {
		t.Fatalf("exact fit preview = %#v", got)
	}
	// A single hidden line uses the singular form.
	if got := ToolOutputPreviewLines([]string{"one", "two", "three", "four"}, 40, 4); got[3] != "+1 line ("+TranscriptHint+")" {
		t.Fatalf("single hidden line preview = %#v", got)
	}
}

// A wrapped row consumes budget rows, and a line that does not fit completely
// counts as hidden while its leading rows stay visible (Rust "a partially
// displayed logical line counts as hidden").
func TestToolOutputPreviewCountsWrappedAndPartialLines(t *testing.T) {
	// Width 4 wraps "abcdefgh" into two rows, leaving one row: "second" shows its
	// first row and is still reported as hidden, and "third" is hidden too, so the
	// hidden count covers both logical lines.
	got := ToolOutputPreviewLines([]string{"abcdefgh", "second", "third"}, 4, 3)
	rows := []string{"abcd", "efgh", "seco"}
	if len(got) != len(rows)+1 {
		t.Fatalf("preview = %#v, want %d rows plus a marker", got, len(rows))
	}
	for i := range rows {
		if got[i] != rows[i] {
			t.Fatalf("preview = %#v, want rows %#v", got, rows)
		}
	}
	// The marker is truncated to the narrow preview width, so only its count
	// survives.
	if marker := got[len(got)-1]; !strings.HasPrefix(marker, "+2 ") {
		t.Fatalf("preview marker = %q, want a two-line hidden count", marker)
	}

	// A line that would need more rows than remain is hidden entirely, and every
	// later line is hidden with it.
	got = ToolOutputPreviewLines([]string{"ok", "abcdefghijkl", "later"}, 4, 3)
	rows = []string{"ok", "abcd", "efgh"}
	if len(got) != len(rows)+1 {
		t.Fatalf("preview = %#v, want %d rows plus a marker", got, len(rows))
	}
	for i := range rows {
		if got[i] != rows[i] {
			t.Fatalf("preview = %#v, want rows %#v", got, rows)
		}
	}
	if marker := got[len(got)-1]; !strings.HasPrefix(marker, "+2 ") {
		t.Fatalf("preview marker = %q, want a two-line hidden count (the overflowing line and the skipped one)", marker)
	}
}

// Long single-line input cannot exceed the row budget: the input is bounded
// before wrapping and the remainder is reported as hidden.
func TestToolOutputPreviewBoundsLongInput(t *testing.T) {
	long := strings.Repeat("x", 5000)
	got := ToolOutputPreviewLines([]string{long}, 20, 1)
	if len(got) != PreviewLines+1 {
		t.Fatalf("preview rows = %d, want %d (%#v)", len(got), PreviewLines+1, got)
	}
	// The marker is truncated to the preview width, so only its prefix survives.
	if marker := got[len(got)-1]; !strings.HasPrefix(marker, "+1 line (") {
		t.Fatalf("preview marker = %q", got[len(got)-1])
	}

	// The 16 KiB input bound keeps a huge line bounded and still reported.
	huge := strings.Repeat("y", MaxPreviewLineBytes*4)
	got = ToolOutputPreviewLines([]string{huge}, 10, 1)
	if len(got) == 0 || !strings.HasPrefix(got[len(got)-1], "+") {
		t.Fatalf("huge preview = %#v", got)
	}
}

// The hidden-line marker is truncated to the preview width, like Rust's
// truncate_line_with_ellipsis_if_overflow.
func TestToolOutputPreviewTruncatesTheMarkerToWidth(t *testing.T) {
	got := ToolOutputPreviewLines([]string{"one", "two", "three", "four"}, 12, 4)
	marker := got[len(got)-1]
	if DisplayWidth(marker) > 12 {
		t.Fatalf("marker = %q (width %d), want at most 12", marker, DisplayWidth(marker))
	}
}
