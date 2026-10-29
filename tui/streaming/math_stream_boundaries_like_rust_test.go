package streaming

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// Rust parity: codex-rs/tui/src/streaming/inline_math_tests.rs
// (`unicode_math_inline_stream_preserves_display_exclusion_boundaries`) together
// with codex-rs/tui/src/markdown_render/streaming.rs's #45501 boundary filter.
//
// Rust keeps its incremental render cache outside every display equation. Go
// renders the stable prefix from a source offset instead of caching a line
// prefix, so the equivalent guarantee is that a display span stays in the
// mutable tail until its closer is committed, and that the streamed transcript
// matches a whole-source render once it is.

const mathStreamTheme = "dark"

func mathStreamDrain(t *testing.T, controller *StreamController, cells []string) []string {
	t.Helper()
	for {
		cell, _ := controller.OnCommitTickBatch(64)
		if cell == nil {
			return cells
		}
		cells = append(cells, cell.RawLines()...)
	}
}

func mathStreamAssertMatchesSource(t *testing.T, cells []string, controller *StreamController, source string, width int, when string) {
	t.Helper()
	got := append(append([]string{}, cells...), controller.CurrentTailLines()...)
	want := renderSourceLines(source, width, mathStreamTheme)
	if len(got) != len(want) {
		t.Fatalf("%s: %d streamed lines, want %d\n got: %q\nwant: %q", when, len(got), len(want), got, want)
	}
	for index := range got {
		if strings.TrimRight(got[index], " ") != strings.TrimRight(want[index], " ") {
			t.Fatalf("%s: line %d = %q, want %q\n got: %q\nwant: %q", when, index, got[index], want[index], got, want)
		}
	}
}

// A display equation must stay in the mutable tail while it streams, so a
// partially rendered display is never committed to scrollback.
func TestStreamedDisplayMathStaysMutableLikeRust(t *testing.T) {
	for _, delimiters := range [][2]string{{"$$", "$$"}, {`\[`, `\]`}} {
		for _, width := range []int{16, 40} {
			name := delimiters[0] + "/" + strconv.Itoa(width)
			t.Run(name, func(t *testing.T) {
				controller := NewStreamControllerWithTheme(width, mathStreamTheme)
				controller.core.now = func() time.Time { return time.Unix(0, 0) }
				var cells []string
				source := ""

				controller.Push(delimiters[0] + "\n")
				source += delimiters[0] + "\n"
				cells = mathStreamDrain(t, controller, cells)
				if len(cells) != 0 {
					t.Fatalf("%s: emitted %q before the display closed", delimiters[0], cells)
				}

				controller.Push("\\frac{a}{b}\n")
				source += "\\frac{a}{b}\n"
				cells = mathStreamDrain(t, controller, cells)
				if len(cells) != 0 {
					t.Fatalf("%s: emitted %q for an open display", delimiters[0], cells)
				}
				mathStreamAssertMatchesSource(t, cells, controller, source, width, "open display")

				controller.Push(delimiters[1] + "\n")
				source += delimiters[1] + "\n"
				cells = mathStreamDrain(t, controller, cells)
				mathStreamAssertMatchesSource(t, cells, controller, source, width, "closed display")

				controller.Push("\nAfter $\\alpha$.\n")
				source += "\nAfter $\\alpha$.\n"
				cells = mathStreamDrain(t, controller, cells)
				mathStreamAssertMatchesSource(t, cells, controller, source, width, "inline math after display")
				if joined := collapseMathStreamSpaces(strings.Join(cells, "\n")); !strings.Contains(joined, "After α.") {
					t.Fatalf("%s: inline math after the display did not render: %q", delimiters[0], joined)
				}
			})
		}
	}
}

// Resizing while a display equation is still open must keep the whole span in
// the tail, and the following closer must render at the new width.
func TestStreamedDisplayMathSurvivesResizeLikeRust(t *testing.T) {
	controller := NewStreamControllerWithTheme(40, mathStreamTheme)
	controller.core.now = func() time.Time { return time.Unix(0, 0) }
	var cells []string

	controller.Push("$$\n\\frac{a}{b}\n")
	source := "$$\n\\frac{a}{b}\n"
	cells = mathStreamDrain(t, controller, cells)
	if len(cells) != 0 {
		t.Fatalf("emitted %q for an open display", cells)
	}

	controller.SetWidth(16)
	cells = mathStreamDrain(t, controller, cells)
	if len(cells) != 0 {
		t.Fatalf("resize emitted %q for an open display", cells)
	}
	mathStreamAssertMatchesSource(t, cells, controller, source, 16, "resized open display")

	controller.Push("$$\nAfter $\\alpha$.\n")
	source += "$$\nAfter $\\alpha$.\n"
	cells = mathStreamDrain(t, controller, cells)
	mathStreamAssertMatchesSource(t, cells, controller, source, 16, "resized closed display")
	if joined := collapseMathStreamSpaces(strings.Join(cells, "\n")); !strings.Contains(joined, "After α.") {
		t.Fatalf("inline math after the resize did not render: %q", joined)
	}
}

// collapseMathStreamSpaces compares rendered text ignoring wrapping and
// padding, so a narrow or resized transcript still matches the same content.
func collapseMathStreamSpaces(text string) string {
	return strings.Join(strings.Fields(text), " ")
}
