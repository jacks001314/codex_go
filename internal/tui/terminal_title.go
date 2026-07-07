package tui

import "strings"

// Rust parity: codex-rs/tui/src/terminal_title.rs.

const MaxTerminalTitleChars = 240

func SanitizeTerminalTitle(title string) string {
	var out strings.Builder
	wrote := 0
	pendingSpace := false
	for _, r := range title {
		if isTitleWhitespace(r) {
			pendingSpace = out.Len() > 0
			continue
		}
		if isDisallowedTerminalTitleRune(r) {
			continue
		}
		if pendingSpace && wrote < MaxTerminalTitleChars-1 {
			out.WriteRune(' ')
			wrote++
			pendingSpace = false
		}
		if wrote >= MaxTerminalTitleChars {
			break
		}
		out.WriteRune(r)
		wrote++
	}
	return out.String()
}

func TerminalTitleOSC(title string) (string, bool) {
	title = SanitizeTerminalTitle(title)
	if title == "" {
		return "", false
	}
	return "\x1b]0;" + title + "\x07", true
}

func ClearTerminalTitleOSC() string {
	return "\x1b]0;\x07"
}

func isTitleWhitespace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\v' || r == '\f'
}

func isDisallowedTerminalTitleRune(r rune) bool {
	if r < 0x20 || r == 0x7f {
		return true
	}
	return r == 0x00AD ||
		r == 0x034F ||
		r == 0x061C ||
		r == 0x180E ||
		(r >= 0x200B && r <= 0x200F) ||
		(r >= 0x202A && r <= 0x202E) ||
		(r >= 0x2060 && r <= 0x206F) ||
		(r >= 0xFE00 && r <= 0xFE0F) ||
		r == 0xFEFF ||
		(r >= 0xFFF9 && r <= 0xFFFB)
}
