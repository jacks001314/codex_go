package bottompane

import (
	"strings"
	"testing"

	"codex_go/tui"
)

// Rust's native status line is a HyperlinkLine: the pull-request span keeps its
// underline and is painted with an OSC-8 destination. RenderStyledWithHyperlinks
// is the string equivalent used by the live footer and the picker preview.
func TestStatusLineRenderStyledWithHyperlinksLikeRust(t *testing.T) {
	line, ok := StatusLineFromSegments([]StatusLineSegment{
		{Item: StatusLineModelName, Text: "gpt-5"},
		{Item: StatusLinePullRequestNumber, Text: "PR #42"},
	}, true)
	if !ok {
		t.Fatal("expected a status line")
	}
	const url = "https://example.com/pr/42"
	rendered := line.RenderStyledWithHyperlinks(url, "", nil, false)
	if !strings.Contains(rendered, "\x1b]8;;"+url+"\x07") {
		t.Fatalf("missing OSC-8 destination: %q", rendered)
	}
	if !strings.Contains(rendered, statusLineUnderlineSGR+"PR #42") {
		t.Fatalf("pull-request span lost its underline: %q", rendered)
	}
	if visible := tui.StripOSC8(rendered); strings.Contains(visible, "\x1b]8;;") {
		t.Fatalf("unterminated hyperlink: %q", visible)
	}

	unsafe := line.RenderStyledWithHyperlinks("file:///tmp/pr", "", nil, false)
	if strings.Contains(unsafe, "\x1b]8;;") {
		t.Fatalf("unsafe destination produced a link: %q", unsafe)
	}
}
