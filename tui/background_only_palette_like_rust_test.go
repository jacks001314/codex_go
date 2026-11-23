package tui

import (
	"strings"
	"testing"
	"time"
)

// Rust #63191: a terminal that answers only the background query keeps its
// shading, while foreground-dependent styles fall back to the terminal default.
func TestBackgroundOnlyPaletteKeepsShadingLikeRust(t *testing.T) {
	restore := SetDefaultTerminalColorsForTest(&DefaultColors{
		BG:                RGBColor{R: 20, G: 20, B: 20},
		ForegroundMissing: true,
	})
	defer restore()

	if _, ok := DefaultForegroundRGB(); ok {
		t.Fatal("background-only palette reported a foreground")
	}
	background, ok := DefaultBackgroundRGB()
	if !ok || background != (RGB{R: 20, G: 20, B: 20}) {
		t.Fatalf("background = %#v, %v", background, ok)
	}
	quiet := QuietWorkspaceStyle(ColorTrue)
	if quiet.Background.RGB == nil && quiet.Background.Index == nil {
		t.Fatalf("quiet workspace style lost its shading: %#v", quiet)
	}
	if quiet.Foreground != (TerminalColor{}) {
		t.Fatalf("quiet workspace foreground = %#v, want the terminal default", quiet.Foreground)
	}
	if got := SecondaryTextStyle(ColorTrue); got.Foreground != (TerminalColor{}) {
		t.Fatalf("secondary text foreground = %#v, want the terminal default", got.Foreground)
	}
	if got := ReadableColorOn(TerminalColor{}, nil, ColorTrue); got != (TerminalColor{}) {
		t.Fatalf("ReadableColorOn(Reset) = %#v, want Reset", got)
	}
	if glyph, ok := shimmerIndicatorGlyph("•", time.Now()); !ok || !strings.Contains(glyph, "•") {
		t.Fatalf("shimmer glyph = %q, %v", glyph, ok)
	}
	if history := HistoryPromptSGR(ColorTrue); !strings.Contains(history, "48;2;") {
		t.Fatalf("history prompt = %q, want a shaded fill", history)
	}
}

// ParseDefaultColors keeps a background-only reply, matching the startup probe
// that stores it when the foreground never answers.
func TestParseDefaultColorsKeepsBackgroundOnlyLikeRust(t *testing.T) {
	colors, ok := ParseDefaultColors([]byte("\x1b]11;rgb:1111/1111/1111\x07"))
	if !ok || !colors.ForegroundMissing || colors.BG != (RGBColor{R: 17, G: 17, B: 17}) {
		t.Fatalf("background-only colors = %#v, %v", colors, ok)
	}
	if _, ok := ParseDefaultColors([]byte("\x1b]10;rgb:eeee/eeee/eeee\x1b\\")); ok {
		t.Fatal("a foreground-only reply must not parse")
	}
}
