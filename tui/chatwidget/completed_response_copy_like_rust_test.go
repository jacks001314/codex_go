package chatwidget

import (
	"testing"

	codextui "codex_go/tui"
)

// Rust #48549 (codex-rs/tui/src/chatwidget/tests/copy_export_picker_tests.rs
// ::completed_response_copy_preserves_markdown_line_endings): copying a
// completed response must keep its Markdown line endings, so hard breaks and
// spaces inside code survive the /copy payload instead of being stripped.
func TestCompletedResponseCopyPreservesMarkdownLineEndings(t *testing.T) {
	const markdown = "Hard break:  \nstarts a new line.\n\n```text\ncode with trailing spaces  \n```\n\ntrailing hard break  "

	text, ok := LastAssistantMarkdown([]codextui.Message{{Role: codextui.RoleAssistant, Text: markdown}})
	if !ok {
		t.Fatal("a completed response with content must be copyable")
	}
	targets := CopyTargetsFromMarkdown(text)
	if len(targets) == 0 || targets[0].ID != CopyTargetWholeID {
		t.Fatalf("targets = %#v, want whole response first", targets)
	}
	if targets[0].Text != markdown {
		t.Fatalf("whole response copy = %q, want %q", targets[0].Text, markdown)
	}
}

// The completed response is normalized like Rust's `str::lines` pass: the final
// line ending is optional, trailing blank lines go away, and the last line's
// hard break stays.
func TestCompletedResponseCopyDropsOnlyTrailingBlankLines(t *testing.T) {
	const markdown = "answer  \n"
	for input, want := range map[string]string{
		markdown:          "answer  ",
		"answer\r\n\r\n":  "answer",
		"answer\n\n\n":    "answer",
		"  \n":            "",
		"keep \r inner\r": "keep \r inner\r",
	} {
		text, ok := LastAssistantMarkdown([]codextui.Message{{Role: codextui.RoleAssistant, Text: input}})
		if want == "" {
			if ok {
				t.Fatalf("blank response %q = %q, want not copyable", input, text)
			}
			continue
		}
		if !ok || text != want {
			t.Fatalf("response %q = %q ok=%v, want %q", input, text, ok, want)
		}
	}
}
