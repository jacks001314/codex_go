package tui

import (
	"strings"
	"unicode/utf8"
)

// Rust parity: codex-rs/tui/src/insert_history.rs (#46710).
//
// Restored history rows carry untrusted tool output, so control characters are
// filtered before the text reaches the terminal. SGR styling and the OSC-8
// hyperlinks the history cells themselves add stay intact.
func SanitizeHistoryText(value string) string {
	if value == "" {
		return value
	}
	var out strings.Builder
	out.Grow(len(value))
	for index := 0; index < len(value); {
		r, size := utf8.DecodeRuneInString(value[index:])
		switch {
		case r == '\n':
			out.WriteRune(r)
			index += size
		case r == '\t':
			out.WriteByte(' ')
			index += size
		case r == 0x1b:
			if end, ok := trustedEscapeEnd(value[index:]); ok {
				out.WriteString(value[index : index+end])
				index += end
				continue
			}
			// Drop the escape introducer and keep the remaining printable
			// characters, matching ratatui's control-character filter.
			index += size
		case r == '\r':
			index += size
		case r < 0x20 || (r >= 0x7f && r <= 0x9f):
			index += size
		default:
			out.WriteRune(r)
			index += size
		}
	}
	return out.String()
}

// SanitizeHistoryLines applies SanitizeHistoryText to every row.
func SanitizeHistoryLines(lines []string) []string {
	if len(lines) == 0 {
		return lines
	}
	out := make([]string, len(lines))
	for index, line := range lines {
		out[index] = SanitizeHistoryText(line)
	}
	return out
}

// trustedEscapeEnd reports the end of an SGR color sequence or an OSC-8
// hyperlink, the only escapes a history row may carry.
func trustedEscapeEnd(value string) (int, bool) {
	if len(value) < 3 || value[0] != 0x1b {
		return 0, false
	}
	switch value[1] {
	case '[':
		index := 2
		for index < len(value) {
			switch {
			case value[index] >= '0' && value[index] <= '9', value[index] == ';':
				index++
			case value[index] == 'm':
				return index + 1, true
			default:
				return 0, false
			}
		}
	case ']':
		if len(value) >= 5 && value[2] == '8' && value[3] == ';' {
			for index := 4; index < len(value); index++ {
				if value[index] == 0x07 {
					return index + 1, true
				}
			}
		}
	}
	return 0, false
}
