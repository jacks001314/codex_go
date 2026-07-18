package tui

import (
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTextFormattingHelpers(t *testing.T) {
	if got := CapitalizeFirst("éclair"); got != "Éclair" {
		t.Fatalf("CapitalizeFirst unicode = %q, want Éclair", got)
	}
	if got := TruncateText("abcdef", 4); got != "a..." {
		t.Fatalf("TruncateText = %q, want a...", got)
	}
	if got := TruncateText("abcdef", 2); got != "ab" {
		t.Fatalf("TruncateText short max = %q, want ab", got)
	}
	if got := TruncateText("e\u0301e\u0301e\u0301e\u0301", 3); got != "..." {
		t.Fatalf("TruncateText combining ellipsis = %q, want ...", got)
	}
	if got := TruncateText("e\u0301e\u0301e\u0301e\u0301", 2); got != "e\u0301e\u0301" {
		t.Fatalf("TruncateText combining short max = %q", got)
	}

	formatted, ok := FormatJSONCompact(`{"b":2,"a":{"x":1}}`)
	if !ok {
		t.Fatal("FormatJSONCompact ok = false")
	}
	if want := `{"a": {"x": 1}, "b": 2}`; formatted != want {
		t.Fatalf("FormatJSONCompact = %q, want %q", formatted, want)
	}
	if _, ok := FormatJSONCompact(`not json`); ok {
		t.Fatal("FormatJSONCompact invalid ok = true")
	}

	path := filepath.Join("root", "alpha", "beta", "gamma", "delta.txt")
	truncated := CenterTruncatePath(path, 24)
	if !strings.Contains(truncated, "\u2026") || !strings.HasSuffix(truncated, filepath.Join("gamma", "delta.txt")) {
		t.Fatalf("CenterTruncatePath = %q", truncated)
	}
	sep := string(filepath.Separator)
	longPath := "~" + sep + "hello" + sep + "the" + sep + "fox" + sep + "is" + sep + "very" + sep + "fast"
	if got, want := CenterTruncatePath(longPath, 24), "~"+sep+"hello"+sep+"the"+sep+"…"+sep+"very"+sep+"fast"; got != want {
		t.Fatalf("CenterTruncatePath long = %q, want %q", got, want)
	}
	windowsStyle := "C:" + sep + "Users" + sep + "codex" + sep + "Projects" + sep + "super" + sep + "long" + sep + "windows" + sep + "path" + sep + "file.txt"
	if got, want := CenterTruncatePath(windowsStyle, 36), "C:"+sep+"Users"+sep+"codex"+sep+"…"+sep+"path"+sep+"file.txt"; got != want {
		t.Fatalf("CenterTruncatePath windows = %q, want %q", got, want)
	}
	longSegment := "~" + sep + "supercalifragilisticexpialidocious"
	if got, want := CenterTruncatePath(longSegment, 18), "~"+sep+"…cexpialidocious"; got != want {
		t.Fatalf("CenterTruncatePath long segment = %q, want %q", got, want)
	}

	if got := ProperJoin(nil); got != "" {
		t.Fatalf("ProperJoin nil = %q, want empty", got)
	}
	if got := ProperJoin([]string{"a", "b"}); got != "a and b" {
		t.Fatalf("ProperJoin two = %q, want a and b", got)
	}
	if got := ProperJoin([]string{"a", "b", "c"}); got != "a, b and c" {
		t.Fatalf("ProperJoin three = %q, want a, b and c", got)
	}
}

func TestTerminalTitleSanitizesAndBuildsOSC(t *testing.T) {
	got := SanitizeTerminalTitle(" hello\t\nworld \x1b \u200b end ")
	if got != "hello world end" {
		t.Fatalf("SanitizeTerminalTitle = %q", got)
	}

	long := SanitizeTerminalTitle(strings.Repeat("a", MaxTerminalTitleChars+20))
	if utf8.RuneCountInString(long) != MaxTerminalTitleChars {
		t.Fatalf("sanitized title length = %d, want %d", utf8.RuneCountInString(long), MaxTerminalTitleChars)
	}

	osc, ok := TerminalTitleOSC("Codex")
	if !ok || osc != "\x1b]0;Codex\x07" {
		t.Fatalf("TerminalTitleOSC = %q ok=%v", osc, ok)
	}
	if osc, ok := TerminalTitleOSC("\x1b\u200b"); ok || osc != "" {
		t.Fatalf("TerminalTitleOSC unsafe empty = %q ok=%v", osc, ok)
	}
	if got := ClearTerminalTitleOSC(); got != "\x1b]0;\x07" {
		t.Fatalf("ClearTerminalTitleOSC = %q", got)
	}
}

func TestTerminalHyperlinks(t *testing.T) {
	safe, ok := WebDestination("https://example.com/a\x00b")
	if !ok || safe != "https://example.com/ab" {
		t.Fatalf("WebDestination sanitized = %q ok=%v", safe, ok)
	}
	for _, destination := range []string{"ftp://example.com", "/relative/path", "mailto:test@example.com"} {
		if safe, ok := WebDestination(destination); ok || safe != "" {
			t.Fatalf("WebDestination(%q) = %q ok=%v, want rejected", destination, safe, ok)
		}
	}

	linked := OSC8Hyperlink("https://example.com", "example")
	if !strings.HasPrefix(linked, "\x1b]8;;https://example.com\x07") || !strings.HasSuffix(linked, "\x1b]8;;\x07") {
		t.Fatalf("OSC8Hyperlink = %q", linked)
	}
	if stripped := StripOSC8(linked); stripped != "example" {
		t.Fatalf("StripOSC8 = %q, want example", stripped)
	}
	if got := OSC8Hyperlink("ftp://example.com", "plain"); got != "plain" {
		t.Fatalf("OSC8Hyperlink invalid = %q, want plain", got)
	}

	links := WebLinksInText("see (https://example.com/a), and http://localhost:3000/x.")
	if len(links) != 2 {
		t.Fatalf("WebLinksInText len = %d, want 2: %#v", len(links), links)
	}
	if links[0].Destination != "https://example.com/a" || links[1].Destination != "http://localhost:3000/x" {
		t.Fatalf("WebLinksInText destinations = %#v", links)
	}
}

func TestTerminalPaletteBestColorForLevel(t *testing.T) {
	target := RGB{R: 1, G: 2, B: 3}
	trueColor := BestColorForLevel(target, ColorTrue)
	if trueColor.RGB == nil || *trueColor.RGB != target || trueColor.Index != nil {
		t.Fatalf("true color = %#v", trueColor)
	}

	ansi := BestColorForLevel(RGB{R: 255, G: 0, B: 0}, ColorANSI256)
	if ansi.Index == nil || *ansi.Index != 9 || ansi.RGB != nil {
		t.Fatalf("ansi color = %#v, want index 9", ansi)
	}
	if got := ClosestANSI256(RGB{R: 8, G: 8, B: 8}); got != 232 {
		t.Fatalf("ClosestANSI256 grayscale = %d, want 232", got)
	}

	unknown := BestColorForLevel(target, ColorUnknown)
	if unknown.RGB != nil || unknown.Index != nil {
		t.Fatalf("unknown color = %#v, want empty", unknown)
	}
}
