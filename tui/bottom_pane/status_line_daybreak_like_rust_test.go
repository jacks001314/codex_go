package bottompane

import "testing"

// TestDaybreakItemMetadataLikeRust mirrors the item metadata Rust #49861 adds
// to bottom_pane/status_line_setup.rs, status_line_style.rs,
// status_surface_preview.rs and title_setup.rs.
func TestDaybreakItemMetadataLikeRust(t *testing.T) {
	line, ok := ParseStatusLineItem("daybreak")
	if !ok || line != StatusLineDaybreak {
		t.Fatalf("ParseStatusLineItem(daybreak) = %v ok=%v", line, ok)
	}
	if id := StatusLineItemID(line); id != "daybreak" {
		t.Fatalf("StatusLineItemID(daybreak) = %q", id)
	}
	if preview := StatusLineItemPreviewItem(line); preview != StatusPreviewDaybreak {
		t.Fatalf("StatusLineItemPreviewItem(daybreak) = %q", preview)
	}
	if got := StatusLineItemDescription(line, DefaultStatusSurfacePreviewData()); got != "Whether Daybreak is enabled for this thread" {
		t.Fatalf("description = %q", got)
	}
	if got := StatusLineAccentForItem(line); got != StatusLineAccentMode {
		t.Fatalf("accent = %q, want %q", got, StatusLineAccentMode)
	}
	title, ok := ParseTerminalTitleItem("daybreak")
	if !ok || title != TerminalTitleDaybreak {
		t.Fatalf("ParseTerminalTitleItem(daybreak) = %v ok=%v", title, ok)
	}
	if preview, ok := title.PreviewItem(); !ok || preview != StatusPreviewDaybreak {
		t.Fatalf("terminal title preview = %q ok=%v", preview, ok)
	}
	if got := statusSurfacePlaceholderForTest(StatusPreviewDaybreak); got != "Daybreak off" {
		t.Fatalf("preview placeholder = %q", got)
	}
}

// statusSurfacePlaceholderForTest reaches the unexported placeholder table
// through the exported preview data, so the test does not depend on internals.
func statusSurfacePlaceholderForTest(item StatusSurfacePreviewItem) string {
	value, _ := DefaultStatusSurfacePreviewData().ValueFor(item)
	return value
}
