package tui

import (
	"strings"
	"testing"

	"codex_go/turn"
)

// TestTaskMentionCodecMatchesRust covers the Rust task_mentions link codec:
// valid_thread_path's shape rules, parse_task_link's bound/escape handling, and
// decode_task_links' element-exact restoration.
func TestTaskMentionCodecMatchesRust(t *testing.T) {
	title := "Fix ]( parser \\ safely"
	link := FormatTaskLink(title, "thread://task-123")
	if link != `[@Fix \]\( parser \\ safely](thread://task-123)` {
		t.Fatalf("FormatTaskLink = %q", link)
	}
	parsedTitle, parsedPath, end, ok := ParseTaskLink(link, 0)
	if !ok || parsedTitle != title || parsedPath != "thread://task-123" || end != len(link) {
		t.Fatalf("ParseTaskLink = %q %q %d %v", parsedTitle, parsedPath, end, ok)
	}

	tooLong := "[@" + strings.Repeat("x", MaxTaskTitleChars+1) + "](thread://task-123)"
	if _, _, _, ok := ParseTaskLink(tooLong, 0); ok {
		t.Fatalf("over-long title parsed: %q", tooLong)
	}
	for _, path := range []string{"thread://", "thread://../settings", "thread://task?target", "thread://" + strings.Repeat("x", 65)} {
		if _, ok := ValidThreadPath(path); ok {
			t.Fatalf("ValidThreadPath(%q) = true", path)
		}
	}
	if threadID, ok := ValidThreadPath("thread://task-123"); !ok || threadID != "task-123" {
		t.Fatalf("ValidThreadPath valid case = %q %v", threadID, ok)
	}

	// A non-char-boundary element or an element that does not cover the whole
	// link leaves the text untouched.
	malformed := "[@é](thread://task-123)"
	for _, end := range []int{3, len(malformed) + 1} {
		decodedText, decodedElements := DecodeTaskLinks(malformed, []turn.TextElement{{
			ByteRange: turn.ByteRange{Start: 0, End: uint(end)},
		}})
		if decodedText != malformed || len(decodedElements) != 0 {
			t.Fatalf("malformed decode(end=%d) = %q %#v", end, decodedText, decodedElements)
		}
	}

	// Only an element whose placeholder matches the parsed title decodes.
	literal := "[@actual](thread://task-456)"
	placeholder := "@actual"
	decodedText, decodedElements := DecodeTaskLinks(literal+" "+literal, []turn.TextElement{
		{ByteRange: turn.ByteRange{Start: 0, End: uint(len(literal))}},
		{
			ByteRange:   turn.ByteRange{Start: uint(len(literal) + 1), End: uint(len(literal)*2 + 1)},
			Placeholder: &placeholder,
		},
	})
	if decodedText != literal+" @actual" {
		t.Fatalf("literal decode = %q", decodedText)
	}
	if len(decodedElements) != 2 {
		t.Fatalf("decoded elements = %#v", decodedElements)
	}
	if got := decodedText[decodedElements[1].ByteRange.Start:decodedElements[1].ByteRange.End]; got != "@actual" {
		t.Fatalf("decoded element range = %q", got)
	}
	// Text without a task path is returned as-is.
	if text, elements := DecodeTaskLinks("hello", nil); text != "hello" || elements != nil {
		t.Fatalf("no-op decode = %q %#v", text, elements)
	}
}
