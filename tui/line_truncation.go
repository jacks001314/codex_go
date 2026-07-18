package tui

import "github.com/mattn/go-runewidth"

// Rust parity: codex-rs/tui/src/line_truncation.rs.

func DisplayWidth(text string) int {
	return runewidth.StringWidth(text)
}

func TruncateToWidth(text string, maxWidth int) string {
	if maxWidth <= 0 || text == "" {
		return ""
	}
	used := 0
	out := make([]rune, 0, len(text))
	for _, r := range text {
		width := runewidth.RuneWidth(r)
		if used+width > maxWidth {
			break
		}
		out = append(out, r)
		used += width
	}
	return string(out)
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
