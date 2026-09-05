package markdown

import (
	"bytes"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

// RenderClipboardHTML converts assistant Markdown into the HTML fragment used
// for rich-text clipboard destinations (Rust #42847 clipboard_html.rs). The
// source Markdown remains the plain-text representation; GFM extensions keep
// tables, strikethrough, and task lists intact.
func RenderClipboardHTML(source string) string {
	if source == "" {
		return ""
	}
	renderer := goldmark.New(goldmark.WithExtensions(extension.GFM))
	var buf bytes.Buffer
	if err := renderer.Convert([]byte(source), &buf); err != nil {
		return ""
	}
	return buf.String()
}
