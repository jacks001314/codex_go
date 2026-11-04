package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
	"github.com/rivo/uniseg"
)

// Rust parity: codex-rs/tui/src/line_truncation.rs.

func DisplayWidth(text string) int {
	if strings.Contains(text, "\x1b") {
		return ansi.StringWidth(text)
	}
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
	if strings.Contains(text, "\x1b") {
		// uniseg may split a long CSI parameter sequence at the width boundary.
		// Let the ANSI-aware truncator keep the escape intact and close any
		// active style at the cut.
		if ansi.StringWidth(text) <= maxWidth {
			return text
		}
		return ansi.Truncate(text, maxWidth, "")
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
	if strings.Contains(text, "\x1b") {
		if ansi.StringWidth(text) <= maxWidth {
			return text
		}
		if maxWidth == 1 {
			return "…"
		}
		return ansi.Truncate(text, maxWidth, "…")
	}
	if DisplayWidth(text) <= maxWidth {
		return text
	}
	if maxWidth == 1 {
		return "…"
	}
	return TruncateToWidth(text, maxWidth-1) + "…"
}
