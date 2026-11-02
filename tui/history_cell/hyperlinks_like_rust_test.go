package historycell

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/tui"
)

// Rust parity: codex-rs/tui/src/history_cell/patches.rs #58446 (a795f961be):
// ViewImageHistoryCell::link_local_image, display_hyperlink_lines and
// transcript_hyperlink_lines keep the image link on the path columns while the
// compact row truncates and the transcript path wraps.

func writeHyperlinkCellPNG(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "diagram.png")
	if err := os.WriteFile(path, []byte("png"), 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}
	return path
}

func linkedText(t *testing.T, line HyperlinkLine) string {
	t.Helper()
	linked := ""
	for _, link := range line.Links {
		start := byteOffsetForColumn(line.Text, link.Start)
		end := byteOffsetForColumn(line.Text, link.End)
		if end > len(line.Text) || start > end {
			t.Fatalf("link range %d..%d outside %q", link.Start, link.End, line.Text)
		}
		linked += line.Text[start:end]
	}
	return linked
}

func TestViewedImageCompactRowLinksPathLikeRust(t *testing.T) {
	path := writeHyperlinkCellPNG(t)
	cell := NewViewImageToolCallWithID("call-1", path, "").LinkLocalImage()
	if cell.LinkDestination == "" {
		t.Fatal("a local regular image produced no link destination")
	}

	lines := cell.DisplayHyperlinkLines(80)
	if len(lines) != 1 {
		t.Fatalf("compact lines = %#v", lines)
	}
	line := lines[0]
	if line.Text != "\u2022 Viewed image diagram.png" {
		t.Fatalf("compact text = %q", line.Text)
	}
	start := tui.DisplayWidth("\u2022 Viewed image ")
	if len(line.Links) != 1 {
		t.Fatalf("compact links = %#v", line.Links)
	}
	link := line.Links[0]
	if link.Start != start || link.End != tui.DisplayWidth(line.Text) && link.End != tui.DisplayWidth("\u2022 Viewed image diagram.png") {
		t.Fatalf("link columns = %d..%d, want %d..%d", link.Start, link.End, start, tui.DisplayWidth(line.Text))
	}
	if link.Destination != cell.LinkDestination {
		t.Fatalf("link destination = %q, want %q", link.Destination, cell.LinkDestination)
	}
	if got := linkedText(t, line); got != "diagram.png" {
		t.Fatalf("linked text = %q, want the file name", got)
	}

	rendered := cell.DisplayLines(80)
	if len(rendered) != 1 || !strings.Contains(rendered[0], "\x1b]8;;"+cell.LinkDestination+"\x07diagram.png") {
		t.Fatalf("rendered row = %q, want an OSC-8 file link", rendered)
	}
}

func TestViewedImageCompactRowClipsLinkWhenTruncatedLikeRust(t *testing.T) {
	path := writeHyperlinkCellPNG(t)
	cell := NewViewImageToolCallWithID("call-1", path, "").LinkLocalImage()
	start := tui.DisplayWidth("\u2022 Viewed image ")

	narrow := cell.DisplayHyperlinkLines(start + 4)[0]
	if tui.DisplayWidth(narrow.Text) > start+4 {
		t.Fatalf("narrow row = %q, wider than requested", narrow.Text)
	}
	if len(narrow.Links) != 1 {
		t.Fatalf("narrow links = %#v, want the clipped link", narrow.Links)
	}
	if narrow.Links[0].Start != start || narrow.Links[0].End > tui.DisplayWidth(narrow.Text) {
		t.Fatalf("narrow link = %d..%d, row width %d", narrow.Links[0].Start, narrow.Links[0].End, tui.DisplayWidth(narrow.Text))
	}
	// Rust links start..line.width(), so the ellipsis stays part of the link.
	trimmed := strings.TrimSuffix(linkedText(t, narrow), "\u2026")
	if trimmed == "" || !strings.HasPrefix("diagram.png", trimmed) {
		t.Fatalf("narrow linked text = %q, want a truncated file name", linkedText(t, narrow))
	}

	// A width that ends before the path starts drops the link entirely.
	dropped := cell.DisplayHyperlinkLines(start - 1)[0]
	if len(dropped.Links) != 0 {
		t.Fatalf("link survived a truncated path: %#v", dropped.Links)
	}
}

func TestViewedImageTranscriptWrapsLinkedPathLikeRust(t *testing.T) {
	path := writeHyperlinkCellPNG(t)
	cell := NewViewImageToolCallWithID("call-1", path, "").LinkLocalImage()

	const width = 28
	lines := cell.TranscriptHyperlinkLines(width)
	if len(lines) < 2 {
		t.Fatalf("transcript rows = %#v, want the path wrapped", lines)
	}
	if !strings.HasPrefix(lines[0].Text, "\u2022 Viewed image") {
		t.Fatalf("first transcript row = %q, want the bullet indent", lines[0].Text)
	}
	linked := ""
	for index, line := range lines {
		if tui.DisplayWidth(line.Text) > width {
			t.Fatalf("row %d = %q, wider than %d", index, line.Text, width)
		}
		for _, link := range line.Links {
			if link.Destination != cell.LinkDestination {
				t.Fatalf("row %d destination = %q", index, link.Destination)
			}
			if link.End > tui.DisplayWidth(line.Text) {
				t.Fatalf("row %d link ends outside the row: %#v", index, link)
			}
		}
		linked += linkedText(t, line)
	}
	if linked != path {
		t.Fatalf("linked path = %q, want %q", linked, path)
	}

	// At a wide width the transcript stays on one linked row.
	if wide := cell.TranscriptHyperlinkLines(0); len(wide) != 1 || linkedText(t, wide[0]) != path {
		t.Fatalf("wide transcript rows = %#v", wide)
	}
}

func TestClipHyperlinkLineDropsLinksOutsideVisibleTextLikeRust(t *testing.T) {
	line := HyperlinkLine{
		Text:  "hello world",
		Links: []tui.TerminalHyperlink{{Start: 6, End: 11, Destination: "https://example.com"}},
	}
	clipped := ClipHyperlinkLine(line, 6)
	if len(clipped.Links) != 0 {
		t.Fatalf("clipped links = %#v, want none", clipped.Links)
	}
	if clipped.Text != "hello\u2026" {
		t.Fatalf("clipped text = %q", clipped.Text)
	}
	kept := ClipHyperlinkLine(line, 11)
	if len(kept.Links) != 1 || kept.Links[0].Start != 6 || kept.Links[0].End != 11 {
		t.Fatalf("kept links = %#v", kept.Links)
	}
}

func TestRenderHyperlinkLinesKeepsWebLinksLikeRust(t *testing.T) {
	lines := RenderHyperlinkLines([]HyperlinkLine{{
		Text:  "see docs",
		Links: []tui.TerminalHyperlink{{Start: 4, End: 8, Destination: "https://example.com"}},
	}})
	want := "see " + tui.OSC8Hyperlink("https://example.com", "docs")
	if len(lines) != 1 || lines[0] != want {
		t.Fatalf("rendered = %#v, want %q", lines, want)
	}
}
