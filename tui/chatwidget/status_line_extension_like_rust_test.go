package chatwidget

import (
	"strings"
	"testing"

	"codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
)

// TestStatusLineRendererFromFeatureLikeRust maps the extension feature to its
// renderer and defaults to native.
func TestStatusLineRendererFromFeatureLikeRust(t *testing.T) {
	if got := bottompane.StatusLineRendererFromFeature(false); got != bottompane.StatusLineRendererNative {
		t.Fatalf("disabled renderer = %q", got)
	}
	if got := bottompane.StatusLineRendererFromFeature(true); got != bottompane.StatusLineRendererExtension {
		t.Fatalf("enabled renderer = %q", got)
	}
}

// TestStatusLineExtensionsTreeLinksPullRequestLikeRust builds the shared tree:
// segments stay plain text, the pull-request number becomes a link, and an
// unsafe URL leaves it unlinked.
func TestStatusLineExtensionsTreeLinksPullRequestLikeRust(t *testing.T) {
	segments := []bottompane.StatusLineSegment{
		{Item: bottompane.StatusLineModelName, Text: "gpt-5"},
		{Item: bottompane.StatusLinePullRequestNumber, Text: "PR #42"},
	}
	tree := StatusLineExtensionsTree(segments, "https://example.com/pr/42")
	line := tree.RenderLine(1 << 20)
	if line.Text != "gpt-5 · PR #42" {
		t.Fatalf("rendered text = %q", line.Text)
	}
	if len(line.Hyperlinks) != 1 {
		t.Fatalf("hyperlinks = %#v", line.Hyperlinks)
	}
	link := line.Hyperlinks[0]
	if link.Destination != "https://example.com/pr/42" || link.Start != 8 || link.End != 14 {
		t.Fatalf("link = %#v", link)
	}
	rendered := line.Render()
	if !strings.Contains(rendered, "https://example.com/pr/42") {
		t.Fatalf("rendered tree missing link: %q", rendered)
	}
	if strings.Contains(tui.StripOSC8(rendered), "\u001b") {
		t.Fatalf("visible text leaked escapes: %q", tui.StripOSC8(rendered))
	}

	unlinked := StatusLineExtensionsTree(segments, "file:///tmp/x").RenderLine(1 << 20)
	if len(unlinked.Hyperlinks) != 0 {
		t.Fatalf("unsafe url produced links: %#v", unlinked.Hyperlinks)
	}
	if unlinked.Text != "gpt-5 · PR #42" {
		t.Fatalf("unlinked text = %q", unlinked.Text)
	}
}
