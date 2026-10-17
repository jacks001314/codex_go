package tui

import (
	"strconv"
	"strings"
	"testing"
)

// TestReadableColorOnUsesTheDefaultBackgroundLikeRust covers Rust #54315's
// `readable_color_on(color, None)`: a nil surface resolves against the
// terminal's default background, so a low-contrast theme colour is corrected
// instead of being emitted as-is.
func TestReadableColorOnUsesTheDefaultBackgroundLikeRust(t *testing.T) {
	restore := SetDefaultTerminalColorsForTest(&DefaultColors{
		FG: RGBColor{R: 30, G: 30, B: 30},
		BG: RGBColor{R: 255, G: 255, B: 255},
	})
	defer restore()

	// A near-white foreground on a white terminal background would be
	// unreadable; the adapter must darken it to the minimum contrast.
	pale := TerminalColor{RGB: &RGB{R: 245, G: 245, B: 245}}
	adapted := ReadableColorOn(pale, nil, ColorTrue)
	if adapted.RGB == nil {
		t.Fatalf("adapted colour = %#v, want an rgb colour", adapted)
	}
	if got := ContrastRatio(*adapted.RGB, RGB{R: 255, G: 255, B: 255}); got < MinTextContrast {
		t.Fatalf("adapted contrast = %.2f, want >= %.2f", got, MinTextContrast)
	}

	// A colour that already reads well is preserved.
	readable := TerminalColor{RGB: &RGB{R: 191, G: 74, B: 7}}
	if got := ReadableColorOn(readable, nil, ColorTrue); got.RGB == nil || *got.RGB != *readable.RGB {
		t.Fatalf("readable colour = %#v, want it unchanged", got)
	}

	// ANSI-16 entries are left alone (Rust returns the colour untouched).
	index := uint8(4)
	if got := ReadableColorOn(TerminalColor{Index: &index}, nil, ColorTrue); got.Index == nil || *got.Index != index {
		t.Fatalf("ansi-16 colour = %#v, want it unchanged", got)
	}
}

// TestReadableColorOnWithoutAKnownBackgroundKeepsTheRequestedColour covers the
// unknown-palette branch: without a probed background there is nothing to
// contrast against, so the requested colour is returned at full depth.
func TestReadableColorOnWithoutAKnownBackgroundKeepsTheRequestedColour(t *testing.T) {
	restore := SetDefaultTerminalColorsForTest(nil)
	defer restore()

	preferred := RGB{R: 250, G: 250, B: 250}
	got := ReadableColorOn(TerminalColor{RGB: &preferred}, nil, ColorTrue)
	if got.RGB == nil || *got.RGB != preferred {
		t.Fatalf("colour = %#v, want the requested colour", got)
	}
}

// TestThemeScopeForegroundValueReadableAdaptsMarkdownProse covers the Markdown
// half of Rust #54315: every themed Markdown foreground goes through
// `readable_color_on(fg, None)` before it reaches the renderer.
func TestThemeScopeForegroundValueReadableAdaptsMarkdownProse(t *testing.T) {
	restore := SetDefaultTerminalColorsForTest(&DefaultColors{
		FG: RGBColor{R: 30, G: 30, B: 30},
		BG: RGBColor{R: 255, G: 255, B: 255},
	})
	defer restore()

	const scopes = "markup.heading.markdown"
	raw := ThemeScopeForegroundValue("ada", scopes)
	if raw == "" || !strings.HasPrefix(raw, "#") {
		t.Fatalf("raw ada heading colour = %q", raw)
	}
	adapted := ThemeScopeForegroundValueReadable("ada", ColorTrue, scopes)
	if adapted == "" || !strings.HasPrefix(adapted, "#") {
		t.Fatalf("adapted ada heading colour = %q", adapted)
	}
	rawRGB := parseHexRGBForTest(t, raw)
	adaptedRGB := parseHexRGBForTest(t, adapted)
	if ContrastRatio(rawRGB, RGB{R: 255, G: 255, B: 255}) >= MinTextContrast {
		t.Fatalf("ada heading %q already passes contrast; pick a fainter scope for this test", raw)
	}
	if adaptedRGB == rawRGB {
		t.Fatalf("adapted colour = %q, want the contrast-corrected value", adapted)
	}
	if got := ContrastRatio(adaptedRGB, RGB{R: 255, G: 255, B: 255}); got < MinTextContrast {
		t.Fatalf("adapted contrast = %.2f, want >= %.2f", got, MinTextContrast)
	}

	// Inline code keeps the raw syntax colour: the code path never calls the
	// readable variant.
	if got := ThemeScopeForegroundValue("ada", scopes); got != raw {
		t.Fatalf("raw theme value changed to %q", got)
	}
}

func parseHexRGBForTest(t *testing.T, value string) RGB {
	t.Helper()
	if len(value) != 7 || value[0] != '#' {
		t.Fatalf("colour %q is not #rrggbb", value)
	}
	component := func(offset int) uint8 {
		parsed, err := strconv.ParseUint(value[offset:offset+2], 16, 8)
		if err != nil {
			t.Fatalf("colour %q: %v", value, err)
		}
		return uint8(parsed)
	}
	return RGB{R: component(1), G: component(3), B: component(5)}
}
