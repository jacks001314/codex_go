package markdown

import (
	"testing"

	codextui "codex_go/tui"

	"charm.land/glamour/v2"
)

// TestRenderMarkdownReusesCachedRenderer pins the renderer cache: streaming
// frames re-render the same message, and building a glamour renderer per frame
// was the dominant allocation. A reused renderer must produce byte-identical
// output to a freshly built one across every document shape the transcript
// renders, and must not leak state from the previous document.
func TestRenderMarkdownReusesCachedRenderer(t *testing.T) {
	const width = 60
	kind := markdownStyleTranscript
	style := markdownStyleFor(kind)
	level := codextui.DetectStdoutColorLevel()
	documents := []string{
		"plain prose",
		"## Heading\n\n- one\n- two with **bold**\n",
		"| a | b |\n|---|---|\n| 1 | 2 |\n",
		"```go\nfunc main() {}\n```\n",
		"> quote\n\n1. first\n2. second\n",
		"text with [link](https://example.com) and `code`",
	}
	freshRender := func(document string) string {
		t.Helper()
		renderer, err := glamour.NewTermRenderer(
			glamour.WithStyles(style),
			glamour.WithWordWrap(width),
			glamour.WithChromaFormatter(codextui.ChromaFormatterForLevel(level)),
		)
		if err != nil {
			t.Fatalf("new renderer: %v", err)
		}
		out, err := renderer.Render(document)
		if err != nil {
			t.Fatalf("render %q: %v", document, err)
		}
		return out
	}
	for _, document := range documents {
		if _, err := renderMarkdown(kind, style, width, "", level, document); err != nil {
			t.Fatalf("warm render %q: %v", document, err)
		}
	}
	for _, document := range documents {
		got, err := renderMarkdown(kind, style, width, "", level, document)
		if err != nil {
			t.Fatalf("render %q: %v", document, err)
		}
		if want := freshRender(document); got != want {
			t.Fatalf("cached renderer diverged for %q\n got=%q\nwant=%q", document, got, want)
		}
	}
}

// TestStripInlineMarkdownSharedParserIsStateless covers the shared goldmark
// parser: interleaving different cells must not change a cell's plain text.
func TestStripInlineMarkdownSharedParserIsStateless(t *testing.T) {
	cells := []string{
		"snake_case_name",
		"**bold** and _italic_",
		"`code` and ~~struck~~",
		"[label](https://example.com)",
		"plain text",
	}
	want := make([]string, len(cells))
	for index, cell := range cells {
		want[index] = stripInlineMarkdown(cell)
	}
	for round := 0; round < 3; round++ {
		for index, cell := range cells {
			if got := stripInlineMarkdown(cell); got != want[index] {
				t.Fatalf("round %d stripInlineMarkdown(%q) = %q, want %q", round, cell, got, want[index])
			}
		}
	}
}
