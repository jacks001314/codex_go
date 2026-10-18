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

// Mirrors Rust #42847's clipboard safety policy: raw HTML is escaped as text,
// images contribute only alt text, and non-web link destinations stay visible.
func TestRenderClipboardHTMLEscapesHTMLAndDropsImageSources(t *testing.T) {
	source := "<b>raw</b> ![diagram](https://example.com/image.png) [local](/repo/file.txt)"
	html := RenderClipboardHTML(source)
	if strings.Contains(html, "<img") || strings.Contains(html, "image.png") {
		t.Fatalf("clipboard HTML kept an image source:\n%s", html)
	}
	if !strings.Contains(html, "diagram") {
		t.Fatalf("clipboard HTML dropped image alt text:\n%s", html)
	}
	if !strings.Contains(html, "&lt;b&gt;raw&lt;/b&gt;") {
		t.Fatalf("clipboard HTML did not escape raw HTML:\n%s", html)
	}
	if strings.Contains(html, `href="/repo/file.txt"`) {
		t.Fatalf("clipboard HTML kept a non-web link destination:\n%s", html)
	}
	if !strings.Contains(html, "local (/repo/file.txt)") {
		t.Fatalf("clipboard HTML lost the non-web destination text:\n%s", html)
	}
}
