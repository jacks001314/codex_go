package agentsoverview

import (
	"strings"
	"testing"
)

// Mirrors Rust #50431: markdown link destinations stay attached to the prompt
// and last-message previews as the details pane fits them to the pane width,
// and the truncation ellipsis stays unlinked.
func TestTaskDetailsKeepsPreviewHyperlinksLikeRust(t *testing.T) {
	link := "\x1b]8;;https://example.com/docs\x07docs link\x1b]8;;\x07 and trailing text"
	view := New(sampleRows(), "", false)
	view.Rows[0].LastMessage = "see the link"
	view.RenderMarkdown = func(string, int) []string { return []string{link} }

	styled := strings.Join(view.RenderStyled(120, 24), "\n")
	if !strings.Contains(stripTerminalEscapes(styled), "docs link and trailing text") {
		t.Fatalf("details pane dropped hyperlink text:\n%s", styled)
	}
	if !strings.Contains(styled, "\x1b]8;;https://example.com/docs\x07") {
		t.Fatalf("details pane lost the hyperlink destination:\n%q", styled)
	}

	plain := strings.Join(view.Render(120, 24), "\n")
	if strings.Contains(plain, "\x1b]8;;") {
		t.Fatalf("plain details render leaked an OSC-8 hyperlink:\n%q", plain)
	}
	if !strings.Contains(plain, "docs link and trailing text") {
		t.Fatalf("plain details render dropped hyperlink text:\n%s", plain)
	}
}

// The ellipsis marker that replaces the third preview line must not carry the
// dropped line's hyperlink (Rust #50431 keeps truncation ellipses unlinked).
func TestTaskDetailsPromptEllipsisStaysUnlinkedLikeRust(t *testing.T) {
	link := "\x1b]8;;https://example.com/docs\x07docs link\x1b]8;;\x07 and trailing text"
	view := New(sampleRows(), "", false)
	view.RenderMarkdown = func(string, int) []string { return []string{link, link, link} }

	styled := strings.Join(view.RenderStyled(120, 24), "\n")
	found := false
	for _, line := range strings.Split(styled, "\n") {
		if !strings.Contains(line, "\u2026") {
			continue
		}
		found = true
		if strings.Contains(line, "\x1b]8;;") {
			t.Fatalf("ellipsis row kept a hyperlink: %q", line)
		}
	}
	if !found {
		t.Fatalf("prompt ellipsis row missing:\n%s", styled)
	}
}

// A pre-rendered preview line that is wider than the pane is dropped instead of
// being cut through an escape sequence.
func TestRenderLineDropsOverwideRawLineLikeRust(t *testing.T) {
	link := "\x1b]8;;https://example.com/docs\x07docs link\x1b]8;;\x07 and trailing text"
	if got := renderLine("", []span{{text: link, raw: true}}, 200, true); !strings.Contains(stripTerminalEscapes(got), "docs link and trailing text") {
		t.Fatalf("fitting raw hyperlink line was dropped: %q", got)
	}
	got := renderLine("", []span{{text: link, raw: true}}, 8, true)
	if strings.Contains(got, "\x1b]8;;") && !strings.HasSuffix(got, "\x1b]8;;\x07") {
		t.Fatalf("overwide raw hyperlink line was cut mid-sequence: %q", got)
	}
}
