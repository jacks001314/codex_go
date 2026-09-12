package tui

import (
	"strings"
	"testing"
)

// TestTerminalColorResponseRangesMatchesRust covers the Windows replay helper:
// complete valid OSC 10/11 replies are reported as byte ranges, invalid or
// incomplete ones are not, and OSC-looking content inside a bracketed paste is
// ignored.
func TestTerminalColorResponseRangesMatchesRust(t *testing.T) {
	foreground := "\x1b]10;rgb:ffff/0000/1111\x07"
	background := "\x1b]11;rgb:0000/ffff/2222\x1b\\"
	pair := foreground + background

	ranges := terminalColorResponseRanges([]byte(pair))
	if len(ranges) != 2 {
		t.Fatalf("ranges = %#v", ranges)
	}
	if ranges[0][0] != 0 || ranges[0][1] != len(foreground) {
		t.Fatalf("foreground range = %#v, want [0,%d)", ranges[0], len(foreground))
	}
	if ranges[1][0] != len(foreground) || ranges[1][1] != len(pair) {
		t.Fatalf("background range = %#v", ranges[1])
	}

	pasted := "\x1b[200~" + foreground + "\x1b[201~" + pair
	if ranges := terminalColorResponseRanges([]byte(pasted)); len(ranges) != 2 {
		t.Fatalf("pasted ranges = %#v, want only the real replies", ranges)
	}

	if ranges := terminalColorResponseRanges([]byte(foreground + "\x1b]11;rgb:0000")); len(ranges) != 1 {
		t.Fatalf("incomplete reply ranges = %#v", ranges)
	}
	if ranges := terminalColorResponseRanges([]byte("\x1b]10;rgb:zz/00/00\x07")); len(ranges) != 0 {
		t.Fatalf("invalid reply ranges = %#v", ranges)
	}
	oversized := "\x1b]10;rgb:" + strings.Repeat("a", maxTerminalColorResponseBytes) + "\x07"
	if ranges := terminalColorResponseRanges([]byte(oversized)); len(ranges) != 0 {
		t.Fatalf("oversized reply ranges = %#v", ranges)
	}
}

// TestTerminalDefaultColorsFromResponsesMatchesRust covers the pair rule: both
// slots must be present, and the first reply per slot wins.
func TestTerminalDefaultColorsFromResponsesMatchesRust(t *testing.T) {
	pair := "\x1b]10;rgb:eeee/eeee/eeee\x1b\\\x1b]11;rgb:1111/1111/1111\x07"
	colors, ok := terminalDefaultColorsFromResponses([]byte(pair))
	if !ok || colors.FG != (RGBColor{R: 238, G: 238, B: 238}) || colors.BG != (RGBColor{R: 17, G: 17, B: 17}) {
		t.Fatalf("colors = %#v (ok=%v)", colors, ok)
	}

	first := "\x1b]10;rgb:aaaa/aaaa/aaaa\x07\x1b]10;rgb:bbbb/bbbb/bbbb\x07\x1b]11;rgb:1111/1111/1111\x07"
	colors, ok = terminalDefaultColorsFromResponses([]byte(first))
	if !ok || colors.FG != (RGBColor{R: 170, G: 170, B: 170}) {
		t.Fatalf("first-reply colors = %#v (ok=%v)", colors, ok)
	}

	if _, ok := terminalDefaultColorsFromResponses([]byte("\x1b]10;rgb:eeee/eeee/eeee\x07")); ok {
		t.Fatal("a single slot must not resolve the palette")
	}
	if _, ok := terminalDefaultColorsFromResponses([]byte("typed input")); ok {
		t.Fatal("unrelated input must not resolve the palette")
	}
}
