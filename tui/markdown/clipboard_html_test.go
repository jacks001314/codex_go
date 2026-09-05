package markdown

import (
	"strings"
	"testing"
)

func TestRenderClipboardHTMLPreservesRichFormatting(t *testing.T) {
	source := "# Heading\n\n- one\n- two\n\n~~gone~~ and **bold**"
	html := RenderClipboardHTML(source)
	for _, want := range []string{"<h1>Heading</h1>", "<li>one</li>", "<del>gone</del>", "<strong>bold</strong>"} {
		if !strings.Contains(html, want) {
			t.Fatalf("clipboard HTML missing %q:\n%s", want, html)
		}
	}
}

func TestRenderClipboardHTMLTablesAndCode(t *testing.T) {
	source := "| A | B |\n|---|---|\n| 1 | 2 |\n\n```go\npackage main\n```\n"
	html := RenderClipboardHTML(source)
	for _, want := range []string{"<table>", "<pre><code", "package main"} {
		if !strings.Contains(html, want) {
			t.Fatalf("clipboard HTML missing %q:\n%s", want, html)
		}
	}
}
