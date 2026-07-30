package tui

import (
	"strings"

	"github.com/mattn/go-runewidth"
	"github.com/rivo/uniseg"
)

// Rust parity: codex-rs/tui/src/line_truncation.rs.

func DisplayWidth(text string) int {
	width := 0
	graphemes := uniseg.NewGraphemes(text)
	for graphemes.Next() {
		grapheme := graphemes.Str()
		marks := 0
		var base strings.Builder
		for _, r := range grapheme {
			if r == '\uFF9E' || r == '\uFF9F' {
				marks++
				continue
			}
			base.WriteRune(r)
		}
		width += runewidth.StringWidth(base.String()) + marks
	}
	return width
}

func TruncateToWidth(text string, maxWidth int) string {
	if maxWidth <= 0 || text == "" {
		return ""
	}
	used := 0
	var out strings.Builder
	graphemes := uniseg.NewGraphemes(text)
	for graphemes.Next() {
		grapheme := graphemes.Str()
		width := DisplayWidth(grapheme)
		if used+width > maxWidth {
			break
		}
		out.WriteString(grapheme)
		used += width
	}
	return out.String()
}

func TruncateWithEllipsis(text string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	if DisplayWidth(text) <= maxWidth {
		return text
	}
	if maxWidth == 1 {
		return "…"
	}
	return TruncateToWidth(text, maxWidth-1) + "…"
}
