package tui

// Rust parity: codex-rs/tui/src/task_mentions.rs (link codec half). The
// referenced-task context application lives in tui/bottom_pane (it consumes the
// composer's MentionBinding); the codec lives here so mention_codec can decode
// task links without an import cycle.

import (
	"strings"
	"unicode/utf8"

	"codex_go/turn"
)

const (
	// MaxReferencedTasks bounds the referenced tasks per submitted prompt.
	MaxReferencedTasks = 16
	// MaxTaskTitleChars bounds a parsed task-link title.
	MaxTaskTitleChars = 160
	// MaxReferencedThreadIDBytes bounds the summed referenced thread-id bytes.
	MaxReferencedThreadIDBytes = 768
	// TaskMentionRequestHeading is the user-request boundary the referenced-task
	// context block is inserted before.
	TaskMentionRequestHeading = "## My request for Codex:"
)

// TaskMention mirrors Rust task_mentions::TaskMention.
type TaskMention struct {
	ThreadID string
	Title    string
	CWD      string
	Snippet  string
}

// ValidThreadPath mirrors Rust valid_thread_path: a `thread://` link carrying a
// non-empty id of at most 64 ASCII alphanumeric/_/- bytes.
func ValidThreadPath(path string) (string, bool) {
	threadID, ok := strings.CutPrefix(path, "thread://")
	if !ok || threadID == "" || len(threadID) > 64 {
		return "", false
	}
	for index := 0; index < len(threadID); index++ {
		if !isTaskMentionNameByte(threadID[index]) {
			return "", false
		}
	}
	return threadID, true
}

// ParseTaskLink mirrors Rust parse_task_link: parse `[@title](thread://id)`
// starting at the byte offset start. It returns the unescaped title, the path,
// and the exclusive end offset on success.
func ParseTaskLink(text string, start int) (string, string, int, bool) {
	if start < 0 || !UTF8Boundary(text, start) {
		return "", "", 0, false
	}
	remaining, ok := strings.CutPrefix(text[start:], "[@")
	if !ok {
		return "", "", 0, false
	}
	var title strings.Builder
	index := 0
	// Rust iterates at most MAX_TASK_TITLE_CHARS + 1 characters.
	for iteration := 0; iteration <= MaxTaskTitleChars; iteration++ {
		if index >= len(remaining) {
			return "", "", 0, false
		}
		ch, size := utf8.DecodeRuneInString(remaining[index:])
		switch {
		case ch == '\\':
			nextIndex := index + size
			if nextIndex >= len(remaining) {
				return "", "", 0, false
			}
			escaped, escapedSize := utf8.DecodeRuneInString(remaining[nextIndex:])
			title.WriteRune(escaped)
			index = nextIndex + escapedSize
		case ch == ']' && title.Len() > 0:
			suffix, ok := strings.CutPrefix(remaining[index+1:], "(")
			if !ok {
				return "", "", 0, false
			}
			path, _, ok := strings.Cut(suffix, ")")
			if !ok {
				return "", "", 0, false
			}
			if _, valid := ValidThreadPath(path); !valid {
				return "", "", 0, false
			}
			return title.String(), path, start + index + len(path) + 5, true
		case ch == ']':
			return "", "", 0, false
		default:
			title.WriteRune(ch)
			index += size
		}
	}
	return "", "", 0, false
}

// FormatTaskLink mirrors Rust format_task_link: escape the title for the
// Markdown link label and wrap it in the guarded task-link form.
func FormatTaskLink(title string, path string) string {
	escaped := strings.ReplaceAll(title, "\\", "\\\\")
	escaped = strings.ReplaceAll(escaped, "](", "]\\(")
	escaped = strings.ReplaceAll(escaped, "]", "\\]")
	return "[@" + escaped + "](" + path + ")"
}

// DecodeTaskLinks mirrors Rust decode_task_links: render a stored task link back
// as its `@title` mention when a text element covers exactly that link and its
// placeholder matches the title. The message-display path uses this so history
// shows the mention the user typed instead of the guarded link.
func DecodeTaskLinks(text string, elements []turn.TextElement) (string, []turn.TextElement) {
	if !strings.Contains(text, "thread://") {
		return text, elements
	}
	var decoded strings.Builder
	decoded.Grow(len(text))
	decodedElements := make([]turn.TextElement, 0, len(elements))
	offset := 0
	for _, element := range elements {
		start := int(element.ByteRange.Start)
		end := int(element.ByteRange.End)
		if start < offset || !UTF8RangeValid(text, start, end) {
			continue
		}
		value := text[start:end]
		decoded.WriteString(text[offset:start])
		decodedStart := decoded.Len()
		if name, _, linkEnd, ok := ParseTaskLink(text, start); ok && linkEnd == end {
			if placeholder, ok := textElementPlaceholder(element, text); ok {
				if trimmed, ok := strings.CutPrefix(placeholder, "@"); ok && trimmed == name {
					decoded.WriteString("@")
					decoded.WriteString(name)
					decodedElements = append(decodedElements, turn.TextElement{
						ByteRange:   turn.ByteRange{Start: uint(decodedStart), End: uint(decoded.Len())},
						Placeholder: element.Placeholder,
					})
					offset = end
					continue
				}
			}
		}
		decoded.WriteString(value)
		decodedElements = append(decodedElements, turn.TextElement{
			ByteRange:   turn.ByteRange{Start: uint(decodedStart), End: uint(decoded.Len())},
			Placeholder: element.Placeholder,
		})
		offset = end
	}
	decoded.WriteString(text[offset:])
	return decoded.String(), decodedElements
}

// textElementPlaceholder mirrors Rust TextElement::placeholder: the recorded
// placeholder, else the element's own text.
func textElementPlaceholder(element turn.TextElement, text string) (string, bool) {
	if element.Placeholder != nil {
		return *element.Placeholder, true
	}
	start := int(element.ByteRange.Start)
	end := int(element.ByteRange.End)
	if !UTF8RangeValid(text, start, end) {
		return "", false
	}
	return text[start:end], true
}

// UTF8Boundary reports whether index is a valid UTF-8 char boundary in value,
// mirroring Rust's `str::get(index..)` returning None off a boundary.
func UTF8Boundary(value string, index int) bool {
	return index >= 0 && index <= len(value) && (index == 0 || index == len(value) || utf8.RuneStart(value[index]))
}

// UTF8RangeValid mirrors Rust `str::get(start..end)` for a byte range.
func UTF8RangeValid(value string, start int, end int) bool {
	return start >= 0 && start <= end && end <= len(value) && UTF8Boundary(value, start) && UTF8Boundary(value, end)
}

func isTaskMentionNameByte(byteValue byte) bool {
	return byteValue >= 'a' && byteValue <= 'z' ||
		byteValue >= 'A' && byteValue <= 'Z' ||
		byteValue >= '0' && byteValue <= '9' ||
		byteValue == '_' || byteValue == '-'
}
