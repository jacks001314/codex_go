package markdown

import (
	"strings"
	"testing"

	"codex_go/utils"
)

// Rust #48623 (upstream 98072cf5f6): "Preserve empty Markdown list markers in
// the TUI". The renderer emits the pending marker when an empty item ends or a
// nested list starts on a new line, including inside blockquotes, so an empty
// item keeps its marker instead of losing it.
//
// Ported Rust test: `empty_list_items_keep_their_markers` with the
// `empty_list_items` snapshot (markdown_render_tests.rs). The snapshot expects
//
//	["8."]        -> ["8. "]
//	["-\n  -"]    -> ["• ", "    • "]
//	["> -\n>   -"] -> ["> • ", ">     • "]
//
// The Go renderer paints blockquote indentation with a box-drawing bar
// (blockQuoteIndentToken) where Rust uses "> ", a pre-existing Go-vs-Rust
// difference that #48623 does not touch; the marker content itself matches.
func TestEmptyListItemsKeepTheirMarkersLikeRust(t *testing.T) {
	cases := []struct {
		source string
		want   []string
	}{
		{"8.", []string{"8. "}},
		{"1.", []string{"1. "}},
		{"-\n  -", []string{"\u2022 ", "    \u2022 "}},
		{"> -\n>   -", []string{blockQuoteIndentToken + "\u2022 ", blockQuoteIndentToken + "    \u2022 "}},
	}
	for _, tc := range cases {
		lines := []string{}
		for _, line := range renderedLines(t, tc.source) {
			// glamour emits the blank blockquote lines around a quoted list as
			// indent-only lines ("│"); Rust's snapshot has no such line, so they
			// are skipped here and the difference is recorded in the report.
			if strings.TrimSpace(strings.TrimPrefix(utils.StripANSI(line), blockQuoteIndentToken)) == "" {
				continue
			}
			lines = append(lines, line)
		}
		if len(lines) != len(tc.want) {
			t.Fatalf("Render(%q) = %q, want %q", tc.source, lines, tc.want)
		}
		for index, line := range lines {
			plain := utils.StripANSI(line)
			// Rust's plain lines are unpadded ("8. "); the Go renderer pads every
			// rendered line to the wrap width, so the marker keeps its own
			// trailing space and only padding may follow.
			if !strings.HasPrefix(plain, tc.want[index]) {
				t.Fatalf("Render(%q) line %d = %q, want %q (all=%q)", tc.source, index, plain, tc.want[index], lines)
			}
			if strings.TrimSpace(plain[len(tc.want[index]):]) != "" {
				t.Fatalf("Render(%q) line %d = %q, unexpected content after %q", tc.source, index, plain, tc.want[index])
			}
		}
	}
}
