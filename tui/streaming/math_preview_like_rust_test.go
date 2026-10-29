package streaming

import (
	"strings"
	"testing"

	"codex_go/tui"
)

// Rust parity: codex-rs/tui/src/streaming/math_tests.rs
// (`unicode_math_unfinished_display_is_visible_and_reflows`) and the
// `unicode_math_live_source_narrow` snapshot. An unterminated display preview
// shows the wrapped source verbatim, reflows on resize, and never interprets
// TeX or Markdown punctuation.

func trimmedTail(lines []string) []string {
	out := make([]string, len(lines))
	for index, line := range lines {
		out[index] = strings.TrimRight(line, " ")
	}
	return out
}

func TestUnfinishedDisplayMathPreviewShowsSourceAndReflowsLikeRust(t *testing.T) {
	for _, delimiters := range [][2]string{{"$$", "$$"}, {`\[`, `\]`}} {
		t.Run(delimiters[0], func(t *testing.T) {
			controller := NewStreamControllerWithTheme(80, mathStreamTheme)
			controller.Push(delimiters[0][:1])
			if got := strings.Join(trimmedTail(controller.CurrentTailLines()), "\n"); !strings.HasSuffix(got, delimiters[0][:1]) {
				t.Fatalf("after first delimiter: tail = %q", got)
			}
			controller.Push(delimiters[0][1:])
			if got := strings.Join(trimmedTail(controller.CurrentTailLines()), "\n"); !strings.HasSuffix(got, delimiters[0]) {
				t.Fatalf("after opener: tail = %q", got)
			}
			controller.Push("\n \\frac{|a+b+c+d+e+f|}{g+h}")
			for _, width := range []int{80, 20, 80} {
				controller.SetWidth(width)
				preview := trimmedTail(controller.core.preview.lines)
				joined := strings.Join(preview, "\n")
				if !strings.Contains(joined, "frac") {
					t.Fatalf("width %d: preview lost the math source: %q", width, preview)
				}
				for _, line := range preview {
					if got := tui.DisplayWidth(line); got > width {
						t.Fatalf("width %d: preview line %q is %d columns", width, line, got)
					}
				}
				if delimiters[0] == "$$" && width == 20 {
					want := []string{" \\frac{|", "a+b+c+d+e+f|}{g+h}"}
					if joined != strings.Join(want, "\n") {
						t.Fatalf("narrow preview = %q, want %q", preview, want)
					}
				}
			}
			controller.Push("\n" + delimiters[1] + "\n")
			var emitted []string
			emitted = mathStreamDrain(t, controller, emitted)
			_, source := controller.Finalize()
			want := renderSourceLines(source, 80, mathStreamTheme)
			if len(emitted) != len(want) {
				t.Fatalf("%d streamed lines, want %d\n got: %q\nwant: %q", len(emitted), len(want), emitted, want)
			}
			for index := range want {
				if strings.TrimRight(emitted[index], " ") != strings.TrimRight(want[index], " ") {
					t.Fatalf("line %d = %q, want %q", index, emitted[index], want[index])
				}
			}
		})
	}
}

func TestUnfinishedDisplayMathPreviewKeepsTeXVerbatimLikeRust(t *testing.T) {
	cases := []struct{ name, source, want string }{
		{name: "fraction", source: "$$\n\\frac{a_b}{c}", want: `\frac{a_b}{c}`},
		{name: "indented", source: "$$\n  \\frac{a_b}{c}", want: "  \\frac{a_b}{c}"},
		{name: "markdown punctuation", source: "$$\n*a_b*|c", want: "*a_b*|c"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			controller := NewStreamControllerWithTheme(120, mathStreamTheme)
			controller.Push(testCase.source)
			preview := trimmedTail(controller.core.preview.lines)
			if joined := strings.Join(preview, "\n"); !strings.Contains(joined, testCase.want) {
				t.Fatalf("preview = %q, want it to contain %q", preview, testCase.want)
			}
		})
	}
}

func TestShellPIDDollarsDoNotTriggerMathPreviewLikeRust(t *testing.T) {
	controller := NewStreamControllerWithTheme(40, mathStreamTheme)
	controller.Push("echo $$\n")
	controller.Push("plain text")
	if controller.core.hasPendingDisplayMath() {
		t.Fatal("shell PID dollars were tracked as an unterminated display")
	}
	if got := strings.Join(trimmedTail(controller.core.preview.lines), "\n"); !strings.Contains(got, "plain text") {
		t.Fatalf("prose preview = %q, want the plain continuation", got)
	}
}

func TestClosedDisplayMathDropsPreviewLikeRust(t *testing.T) {
	controller := NewStreamControllerWithTheme(40, mathStreamTheme)
	controller.Push("$$\n\\frac{a}{b}")
	if len(controller.core.preview.lines) == 0 {
		t.Fatal("unterminated display has no preview")
	}
	controller.Push("\n$$\n")
	if len(controller.core.preview.lines) != 0 {
		t.Fatalf("closed display kept preview = %q", controller.core.preview.lines)
	}
}
