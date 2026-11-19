package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
	"github.com/rivo/uniseg"
)

// displayWidthReference is the grapheme-by-grapheme implementation DisplayWidth
// used before the printable-ASCII fast path. Every input must keep the same
// width because rendering and cursor math depend on it.
func displayWidthReference(text string) int {
	if strings.Contains(text, "\x1b") {
		return ansi.StringWidth(text)
	}
	width := 0
	graphemes := uniseg.NewGraphemes(text)
	for graphemes.Next() {
		grapheme := graphemes.Str()
		marks := 0
		var base strings.Builder
		for _, r := range grapheme {
			if r == '\uFF9E' || r == '\uFF9F' {
				marks++
				continue
			}
			base.WriteRune(r)
		}
		width += runewidth.StringWidth(base.String()) + marks
	}
	return width
}

func TestDisplayWidthASCIIFastPathMatchesGraphemePathLikeRust(t *testing.T) {
	inputs := []string{
		"",
		"a",
		"function main() { return 42; }",
		"  leading and trailing  ",
		"\x1b[31mred\x1b[0m",
		"\x1b[2mbold-ish\x1b[0m plain",
		"日本語",
		"ｶﾞ",
		"e\u0301",
		"👍🏽",
		"\t",
		"a\tb",
		"mixed 中文 and ascii",
	}
	for _, input := range inputs {
		want := displayWidthReference(input)
		if got := DisplayWidth(input); got != want {
			t.Fatalf("DisplayWidth(%q) = %d, want %d", input, got, want)
		}
	}
}

func TestDisplayWidthASCIIFastPathScalesLikeRust(t *testing.T) {
	const text = "the quick brown fox jumps over the lazy dog 0123456789"
	if got, want := DisplayWidth(text), len(text); got != want {
		t.Fatalf("DisplayWidth(%q) = %d, want %d", text, got, want)
	}
	if printableASCII("a\tb") {
		t.Fatal("printableASCII accepted a tab")
	}
	if printableASCII("café") {
		t.Fatal("printableASCII accepted a non-ASCII byte")
	}
}
