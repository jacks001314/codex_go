package tui

import (
	"testing"
	"time"
)

// TestSummaryShimmerMidpointMatchesRust covers the Rust #43921 midpoint vector:
// the "Working" band produces the exact interpolated brightness levels between
// fg (240,240,240) and bg (16,16,16). Rust #48757 moved the sample to 1100 ms
// (600 ms initial delay + half of the one-second sweep) while keeping the same
// midpoint levels, so the vector is unchanged:
// codex-rs/tui/src/summary_shimmer_tests.rs
// `working_has_overlapping_highlights_without_frame_to_frame_flashes`.
func TestSummaryShimmerMidpointMatchesRust(t *testing.T) {
	fg := RGB{R: 240, G: 240, B: 240}
	bg := RGB{R: 16, G: 16, B: 16}
	spans := SummaryShimmerSpansAt("Working", 1100*time.Millisecond, MotionAnimated, fg, bg, true)
	want := []uint8{128, 156, 212, 240, 212, 156, 128}
	if len(spans) != len(want) {
		t.Fatalf("spans = %#v", spans)
	}
	for i, span := range spans {
		if span.Style != SummaryShimmerColor {
			t.Fatalf("span %d style = %v, want color", i, span.Style)
		}
		if span.Foreground.R != want[i] || span.Foreground.G != want[i] || span.Foreground.B != want[i] {
			t.Errorf("span %d foreground = %#v, want grey %d", i, span.Foreground, want[i])
		}
	}
}

// TestSummaryShimmerReducedMotionAndUnknownPalette covers the Rust fallbacks:
// reduced motion renders the text unstyled, and an unknown palette renders it
// dim instead of animating.
func TestSummaryShimmerReducedMotionAndUnknownPalette(t *testing.T) {
	// Force the unknown-palette path regardless of the host console palette.
	restoreColors := SetDefaultTerminalColorsForTest(nil)
	defer restoreColors()
	fg := RGB{R: 240, G: 240, B: 240}
	bg := RGB{R: 16, G: 16, B: 16}
	reduced := SummaryShimmerSpansAt("Working", time.Second, MotionReduced, fg, bg, true)
	if len(reduced) != 1 || reduced[0].Text != "Working" || reduced[0].Style != SummaryShimmerPlain {
		t.Fatalf("reduced shimmer = %#v", reduced)
	}
	dim := SummaryShimmerSpansAt("Working", time.Second, MotionAnimated, fg, bg, false)
	if len(dim) != 1 || dim[0].Text != "Working" || dim[0].Style != SummaryShimmerDim {
		t.Fatalf("unknown-palette shimmer = %#v", dim)
	}
	// Without installed terminal colors the exported entry point also dims.
	if spans := SummaryShimmer("Working", time.Second, MotionAnimated); len(spans) != 1 || spans[0].Style != SummaryShimmerDim {
		t.Fatalf("unset-palette shimmer = %#v", spans)
	}
	if spans := SummaryShimmer("Working", time.Second, MotionReduced); len(spans) != 1 || spans[0].Style != SummaryShimmerPlain {
		t.Fatalf("reduced unset-palette shimmer = %#v", spans)
	}
}

// TestSummaryShimmerSweepIsSmooth matches Rust's frame-to-frame smoothness
// check and the overlapping-highlight rule. Rust #48757 samples two sweeps and
// the intervening gap at the status row's 32 ms cadence, and raises the allowed
// brightness change to the faster sweep's bound.
func TestSummaryShimmerSweepIsSmooth(t *testing.T) {
	fg := RGB{R: 240, G: 240, B: 240}
	bg := RGB{R: 16, G: 16, B: 16}
	var previous []uint8
	for ms := 0; ms <= 5600; ms += 32 {
		spans := SummaryShimmerSpansAt("Working", time.Duration(ms)*time.Millisecond, MotionAnimated, fg, bg, true)
		brightness := make([]uint8, 0, len(spans))
		for _, span := range spans {
			brightness = append(brightness, span.Foreground.R)
		}
		bright := 0
		over := 0
		for _, value := range brightness {
			if value > 220 {
				bright++
			}
			if value > 160 {
				over++
			}
		}
		if bright > 0 && over < 2 {
			t.Fatalf("at %dms a bright frame had %d overlapping highlights: %v", ms, over, brightness)
		}
		for i, current := range brightness {
			if i >= len(previous) {
				break
			}
			diff := int(current) - int(previous[i])
			if diff < 0 {
				diff = -diff
			}
			// At this speed the cosine band's maximum change over 32 ms is
			// 112 * sin(PI * (13 * 0.032) / 6), rounded up (Rust #48757).
			if diff > 25 {
				t.Fatalf("at %dms brightness jumped by %d: %v -> %v", ms, diff, previous, brightness)
			}
		}
		previous = brightness
	}
}

// TestSummaryShimmerPreservesGraphemes covers Rust's grapheme-cluster check:
// combining characters and emoji ZWJ sequences are single shimmer spans.
func TestSummaryShimmerPreservesGraphemes(t *testing.T) {
	fg := RGB{R: 240, G: 240, B: 240}
	bg := RGB{R: 16, G: 16, B: 16}
	text := "e\u0301\U0001F468\u200D\U0001F469\u200D\U0001F467\u200D\U0001F466\u6f22"
	spans := SummaryShimmerSpansAt(text, 0, MotionAnimated, fg, bg, true)
	var got []string
	for _, span := range spans {
		got = append(got, span.Text)
	}
	want := []string{"e\u0301", "\U0001F468\u200D\U0001F469\u200D\U0001F467\u200D\U0001F466", "\u6f22"}
	if len(got) != len(want) {
		t.Fatalf("graphemes = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("grapheme %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestDefaultTerminalColorsSeam covers the palette cache and its test seam.
func TestDefaultTerminalColorsSeam(t *testing.T) {
	forceUnavailable := SetDefaultTerminalColorsForTest(nil)
	defer forceUnavailable()
	if _, ok := defaultTerminalColors(); ok {
		t.Fatal("unset palette must report unavailable")
	}
	restore := SetDefaultTerminalColorsForTest(&DefaultColors{
		FG: RGBColor{R: 240, G: 240, B: 240},
		BG: RGBColor{R: 16, G: 16, B: 16},
	})
	fg, bg, ok := defaultTerminalColorsRGB()
	if !ok || fg != (RGB{R: 240, G: 240, B: 240}) || bg != (RGB{R: 16, G: 16, B: 16}) {
		t.Fatalf("installed palette = (%#v, %#v, %v)", fg, bg, ok)
	}
	restore()
	if _, ok := defaultTerminalColors(); ok {
		t.Fatal("restored palette must report unavailable")
	}
}

func consoleColorTable() [16]uint32 {
	return [16]uint32{
		0x00000000, 0x00000080, 0x00008000, 0x00008080, 0x00800000, 0x00800080, 0x00808000,
		0x00c0c0c0, 0x00808080, 0x000000ff, 0x0000ff00, 0x0000ffff, 0x00ff0000, 0x00ff00ff,
		0x00ffff00, 0x00ffffff,
	}
}

// TestDecodeConsoleDefaultColorsMatchesRust covers Rust's Windows console
// palette fallback vectors (#43921 / terminal_probe/windows_tests.rs).
func TestDecodeConsoleDefaultColorsMatchesRust(t *testing.T) {
	cases := []struct {
		attributes uint16
		want       DefaultColors
	}{
		{0x21, DefaultColors{FG: RGBColor{R: 128}, BG: RGBColor{G: 128}}},
		{0xe9, DefaultColors{FG: RGBColor{R: 255}, BG: RGBColor{G: 255, B: 255}}},
		{0x43, DefaultColors{FG: RGBColor{R: 0x33, G: 0x22, B: 0x11}, BG: RGBColor{R: 0xcc, G: 0xbb, B: 0xaa}}},
		// COMMON_LVB_REVERSE_VIDEO must not change the decoded attribute indices.
		{0x8021, DefaultColors{FG: RGBColor{R: 128}, BG: RGBColor{G: 128}}},
	}
	colors := consoleColorTable()
	colors[3] = 0x00112233
	colors[4] = 0x00aabbcc
	for _, tc := range cases {
		if got := decodeConsoleDefaultColors(tc.attributes, colors); got != tc.want {
			t.Errorf("decodeConsoleDefaultColors(%#x) = %#v, want %#v", tc.attributes, got, tc.want)
		}
	}
}

// TestTerminalColorProbeQueryMatchesRust pins the combined OSC 10/11 query
// (Rust terminal_probe::default_colors writes the same bytes).
func TestTerminalColorProbeQueryMatchesRust(t *testing.T) {
	want := "\x1b]10;?\x1b\\\x1b]11;?\x1b\\"
	if terminalColorProbeQuery != want {
		t.Fatalf("probe query = %q, want %q", terminalColorProbeQuery, want)
	}
	if terminalColorProbeTimeout != 100*time.Millisecond {
		t.Fatalf("probe timeout = %s, want 100ms (Rust DEFAULT_TIMEOUT)", terminalColorProbeTimeout)
	}
}

// TestSummaryShimmerCadenceLikeRust covers the Rust #48757 cadence against
// codex-rs/tui/src/summary_shimmer.rs: the band is parked at its rest position
// during the 600 ms initial delay, sweeps across the label for one second,
// rests at the end for the remainder of the four-second interval, and repeats.
func TestSummaryShimmerCadenceLikeRust(t *testing.T) {
	fg := RGB{R: 240, G: 240, B: 240}
	bg := RGB{R: 16, G: 16, B: 16}
	levelsAt := func(elapsed time.Duration) []uint8 {
		t.Helper()
		spans := SummaryShimmerSpansAt("Working", elapsed, MotionAnimated, fg, bg, true)
		levels := make([]uint8, 0, len(spans))
		for _, span := range spans {
			if span.Style != SummaryShimmerColor {
				t.Fatalf("span at %v style = %v, want color", elapsed, span.Style)
			}
			levels = append(levels, span.Foreground.R)
		}
		return levels
	}
	same := func(a, b []uint8) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}
	rest := levelsAt(0)
	for i, level := range rest {
		// The band sits before the first grapheme during the delay, so every
		// glyph is the halfway blend of fg and bg.
		if level != 128 {
			t.Fatalf("rest level %d = %d, want 128", i, level)
		}
	}
	// The delay holds the resting frame; the sweep only starts 600 ms in.
	if got := levelsAt(500 * time.Millisecond); !same(got, rest) {
		t.Fatalf("500ms levels = %v, want the resting frame %v", got, rest)
	}
	// Midpoint of the first sweep: the Rust #48757 midpoint vector.
	midpoint := levelsAt(1100 * time.Millisecond)
	want := []uint8{128, 156, 212, 240, 212, 156, 128}
	if !same(midpoint, want) {
		t.Fatalf("1100ms levels = %v, want %v", midpoint, want)
	}
	// One second after the sweep started the band has left the label and the
	// gap holds the resting frame until the next sweep.
	if got := levelsAt(1600 * time.Millisecond); !same(got, rest) {
		t.Fatalf("1600ms levels = %v, want the resting frame %v", got, rest)
	}
	if got := levelsAt(3600 * time.Millisecond); !same(got, rest) {
		t.Fatalf("3600ms levels = %v, want the resting frame %v", got, rest)
	}
	// The next sweep starts four seconds after the previous one.
	if got := levelsAt(5100 * time.Millisecond); !same(got, midpoint) {
		t.Fatalf("5100ms levels = %v, want the next sweep's midpoint %v", got, midpoint)
	}
}
