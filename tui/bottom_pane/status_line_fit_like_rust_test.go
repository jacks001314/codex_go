package bottompane

import (
	"strings"
	"testing"

	"codex_go/tui"
)

func statusLineForFit(items ...StatusLineSegment) StatusLine {
	line, ok := StatusLineFromSegments(items, true)
	if !ok {
		panic("empty status line")
	}
	return line
}

// TestStatusLineFitKeepsEllipsisOutsideHrefLikeRust mirrors Rust #57883: the
// footer fits the status line to the width beside right-hand indicators while
// keeping the ellipsis outside the hyperlink and clipping the link to the
// visible prefix.
func TestStatusLineFitKeepsEllipsisOutsideHrefLikeRust(t *testing.T) {
	line := statusLineForFit(
		StatusLineSegment{Item: StatusLineModelName, Text: "ｶﾞ"},
		StatusLineSegment{Item: StatusLinePullRequestNumber, Text: "PR #42"},
	)
	text, links := line.FitToWidth(8, "https://example.com/pr/42")
	if !strings.HasSuffix(text, "…") {
		t.Fatalf("fitted text lacks ellipsis: %q", text)
	}
	if tui.DisplayWidth(text) > 8 {
		t.Fatalf("fitted width = %d", tui.DisplayWidth(text))
	}
	if len(links) != 1 {
		t.Fatalf("links = %#v", links)
	}
	if links[0].End > tui.DisplayWidth(strings.TrimSuffix(text, "…")) {
		t.Fatalf("link %#v reaches into the ellipsis (%q)", links[0], text)
	}
	if links[0].Destination != "https://example.com/pr/42" {
		t.Fatalf("destination = %q", links[0].Destination)
	}
}

// TestStatusLineFitHandlesArabicPrefixLikeRust covers a right-to-left prefix
// whose grapheme width differs from a naive rune count.
func TestStatusLineFitHandlesArabicPrefixLikeRust(t *testing.T) {
	line := statusLineForFit(
		StatusLineSegment{Item: StatusLineStatus, Text: "لا"},
		StatusLineSegment{Item: StatusLinePullRequestNumber, Text: "PR #42"},
	)
	text, links := line.FitToWidth(8, "https://example.com/pr/42")
	if tui.DisplayWidth(text) > 8 {
		t.Fatalf("fitted width = %d for %q", tui.DisplayWidth(text), text)
	}
	if len(links) != 1 {
		t.Fatalf("links = %#v", links)
	}
	visible := tui.DisplayWidth(strings.TrimSuffix(text, "…"))
	if links[0].End > visible {
		t.Fatalf("link %#v reaches into the ellipsis (%q)", links[0], text)
	}
	if links[0].Start >= links[0].End {
		t.Fatalf("link clipped to nothing: %#v", links[0])
	}
}

// TestStatusLineFitKeepsUnlinkedLineUnchangedLikeRust: without a safe URL the
// status line has no hyperlink and keeps its full text when it fits.
func TestStatusLineFitKeepsUnlinkedLineUnchangedLikeRust(t *testing.T) {
	line := statusLineForFit(
		StatusLineSegment{Item: StatusLineModelName, Text: "gpt-5"},
		StatusLineSegment{Item: StatusLinePullRequestNumber, Text: "PR #7"},
	)
	text, links := line.FitToWidth(80, "")
	if text != "gpt-5 · PR #7" || len(links) != 0 {
		t.Fatalf("unlinked fit = %q %#v", text, links)
	}
	if text, links := line.FitToWidth(80, "file:///tmp/x"); text != "gpt-5 · PR #7" || len(links) != 0 {
		t.Fatalf("unsafe url fit = %q %#v", text, links)
	}
}
