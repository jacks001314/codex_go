package tea

import (
	"reflect"
	"strings"
	"testing"

	codextui "codex_go/tui"
)

// Rust parity: codex-rs/tui/src/bottom_pane/async_questions/state_tests.rs
// `multiline_question_preserves_line_breaks_and_link_destinations` (#51866,
// upstream eef4719927). Upstream renders the same title with LF and CRLF line
// endings and asserts the two rendered buffers are identical, that blank lines
// and the trailing line break survive, and that every piece of the URL keeps the
// complete destination as its OSC-8 target. Go carrier:
// tui/tea/async_questions.go wrapAsyncQuestionTitleLines.

const multilineQuestionURL = "https://example.com/diagnostics?view=full"

func multilineQuestionTitle() string {
	return "Run this command:\n\n```sh\nprintf '診断'\n```\nThen review (" + multilineQuestionURL + ")?\n"
}

type osc8Link struct{ destination, text string }

// osc8LinksInLine returns the OSC-8 hyperlinks embedded in one rendered line,
// in the order they appear (upstream walks the buffer cells and keeps every
// symbol that carries the OSC-8 introducer).
func osc8LinksInLine(line string) []osc8Link {
	const open = "\x1b]8;;"
	const separator = "\x07"
	const close = "\x1b]8;;\x07"
	links := []osc8Link{}
	for cursor := 0; ; {
		start := strings.Index(line[cursor:], open)
		if start < 0 {
			return links
		}
		start += cursor
		rest := line[start+len(open):]
		destinationEnd := strings.Index(rest, separator)
		if destinationEnd < 0 {
			return links
		}
		linked := rest[destinationEnd+len(separator):]
		textEnd := strings.Index(linked, close)
		if textEnd < 0 {
			return links
		}
		links = append(links, osc8Link{destination: rest[:destinationEnd], text: linked[:textEnd]})
		cursor = start + len(open) + destinationEnd + len(separator) + textEnd + len(close)
	}
}

func TestMultilineAsyncQuestionPreservesLineBreaksAndLinkDestinationsLikeRust(t *testing.T) {
	title := multilineQuestionTitle()
	widths := []int{20, 60}
	rendered := map[int]map[string][]string{}
	for _, width := range widths {
		rendered[width] = map[string][]string{}
	}
	for _, lineEnding := range []string{"\n", "\r\n"} {
		for _, width := range widths {
			lines := wrapAsyncQuestionTitleLines(strings.ReplaceAll(title, "\n", lineEnding), width)
			linked := ""
			for _, line := range lines {
				for _, link := range osc8LinksInLine(line) {
					if link.destination != multilineQuestionURL {
						t.Fatalf("line ending %q width %d: link destination = %q, want %q", lineEnding, width, link.destination, multilineQuestionURL)
					}
					linked += link.text
				}
			}
			if linked != multilineQuestionURL {
				t.Fatalf("line ending %q width %d: linked text = %q, want %q", lineEnding, width, linked, multilineQuestionURL)
			}
			stripped := make([]string, 0, len(lines))
			for _, line := range lines {
				plain := codextui.StripOSC8(line)
				if codextui.DisplayWidth(plain) > width {
					t.Fatalf("line ending %q width %d: wrapped row %q is %d columns", lineEnding, width, plain, codextui.DisplayWidth(plain))
				}
				stripped = append(stripped, plain)
			}
			rendered[width][lineEnding] = stripped
		}
	}
	for _, width := range widths {
		if !reflect.DeepEqual(rendered[width]["\n"], rendered[width]["\r\n"]) {
			t.Fatalf("width %d: LF render = %#v, CRLF render = %#v", width, rendered[width]["\n"], rendered[width]["\r\n"])
		}
	}
	want := []string{
		"Run this command:",
		"",
		"```sh",
		"printf '診断'",
		"```",
		"Then review (" + multilineQuestionURL + ")?",
		"",
	}
	if !reflect.DeepEqual(rendered[60]["\n"], want) {
		t.Fatalf("width 60 render = %#v, want %#v", rendered[60]["\n"], want)
	}
}
