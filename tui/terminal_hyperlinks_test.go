package tui

import (
	"strings"
	"testing"
)

// Mirrors Rust #46884's `bare_url_styling_preserves_tabs_and_unicode`: the link
// style is patched over the URL only, and the surrounding bold text, tab, and
// Unicode are restored exactly.
func TestStyleWebURLsInLinePreservesTabsAndUnicodeLikeRust(t *testing.T) {
	const linkSGR = "\x1b[4m\x1b[36m"
	got := StyleWebURLsInLine("\x1b[1m界\t(https://example.com).\x1b[0m", linkSGR)
	want := "\x1b[1m界\t(" + linkSGR + "https://example.com\x1b[0m\x1b[1m).\x1b[0m"
	if got != want {
		t.Fatalf("StyleWebURLsInLine = %q, want %q", got, want)
	}
}

// Glamour underlines the sentence punctuation after a GFM autolink together
// with the URL. The styling pass keeps the URL underlined and moves the suffix
// back into the surrounding prose.
func TestStyleWebURLsInLineDeUnderlinesAutolinkPunctuationLikeRust(t *testing.T) {
	got := StyleWebURLsInLine("\x1b[4mhttps://example.com/a);\x1b[0m", "\x1b[4m")
	if !strings.Contains(got, "\x1b[24m);") {
		t.Fatalf("punctuation stayed underlined: %q", got)
	}
	if !strings.Contains(got, "https://example.com/a\x1b[24m") {
		t.Fatalf("URL itself lost its underline: %q", got)
	}
}

func TestOSC8LinksInLineRanges(t *testing.T) {
	line := "Tip: " + OSC8Hyperlink("https://example.com/docs", "docs") + " end"
	links := OSC8LinksInLine(line)
	if len(links) != 1 {
		t.Fatalf("links = %#v, want one", links)
	}
	if links[0].Start != DisplayWidth("Tip: ") || links[0].End != DisplayWidth("Tip: docs") {
		t.Fatalf("link range = %#v, want columns 5..9", links[0])
	}
	if links[0].Destination != "https://example.com/docs" {
		t.Fatalf("destination = %q", links[0].Destination)
	}

	bare := OSC8LinksInLine("Tip: visit https://example.com/a now")
	if len(bare) != 1 || bare[0].Destination != "https://example.com/a" {
		t.Fatalf("bare URL fallback = %#v", bare)
	}
}
