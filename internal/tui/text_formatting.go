package tui

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

// Rust parity: codex-rs/tui/src/text_formatting.rs.

func CapitalizeFirst(text string) string {
	if text == "" {
		return ""
	}
	runes := []rune(text)
	if runes[0] >= 'a' && runes[0] <= 'z' {
		runes[0] -= 'a' - 'A'
	}
	return string(runes)
}

func TruncateText(text string, maxGraphemes int) string {
	if maxGraphemes <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= maxGraphemes {
		return text
	}
	if maxGraphemes < 3 {
		return string(runes[:maxGraphemes])
	}
	return string(runes[:maxGraphemes-3]) + "..."
}

func FormatJSONCompact(text string) (string, bool) {
	var value any
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		return "", false
	}
	data, err := json.Marshal(value)
	if err != nil {
		return "", false
	}
	return addJSONSeparatorSpaces(string(data)), true
}

func FormatAndTruncateToolResult(text string, maxLines int, lineWidth int) string {
	maxChars := maxLines*lineWidth - maxLines
	if maxChars < 0 {
		maxChars = 0
	}
	if formatted, ok := FormatJSONCompact(text); ok {
		return TruncateText(formatted, maxChars)
	}
	return TruncateText(text, maxChars)
}

func CenterTruncatePath(path string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	if DisplayWidth(path) <= maxWidth {
		return path
	}
	separator := string(filepath.Separator)
	segments := strings.Split(path, separator)
	if len(segments) <= 2 {
		return frontTruncate(path, maxWidth)
	}
	for left := len(segments) - 2; left >= 1; left-- {
		candidate := strings.Join(append(append([]string{}, segments[:left]...), append([]string{"…"}, segments[len(segments)-2:]...)...), separator)
		if DisplayWidth(candidate) <= maxWidth {
			return candidate
		}
	}
	candidate := strings.Join([]string{segments[0], "…", segments[len(segments)-1]}, separator)
	if DisplayWidth(candidate) <= maxWidth {
		return candidate
	}
	return frontTruncate(path, maxWidth)
}

func ProperJoin(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	case 2:
		return items[0] + " and " + items[1]
	default:
		return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
	}
}

func addJSONSeparatorSpaces(text string) string {
	var out strings.Builder
	inString := false
	escaped := false
	for _, r := range text {
		out.WriteRune(r)
		if escaped {
			escaped = false
			continue
		}
		if inString && r == '\\' {
			escaped = true
			continue
		}
		if r == '"' {
			inString = !inString
			continue
		}
		if !inString && (r == ':' || r == ',') {
			out.WriteRune(' ')
		}
	}
	return strings.ReplaceAll(out.String(), ", }", "}")
}

func frontTruncate(text string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	if DisplayWidth(text) <= maxWidth {
		return text
	}
	if maxWidth == 1 {
		return "…"
	}
	kept := []rune{}
	used := 1
	for _, r := range reverseRunes([]rune(text)) {
		width := DisplayWidth(string(r))
		if used+width > maxWidth {
			break
		}
		kept = append([]rune{r}, kept...)
		used += width
	}
	return "…" + string(kept)
}

func reverseRunes(in []rune) []rune {
	out := append([]rune(nil), in...)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}
