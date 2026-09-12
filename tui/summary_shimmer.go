package tui

import (
	"math"
	"time"

	"github.com/rivo/uniseg"
)

// Rust parity: codex-rs/tui/src/summary_shimmer.rs (#43921). Smooth,
// whole-grapheme status shimmer with a two-second sweep. Only brightness
// changes: the moving band uses the terminal foreground, while the remaining
// text blends halfway into the background. The wave spans at least six
// terminal columns so short labels do not flash one letter at a time. Unknown
// palettes use static dim text instead of a stepped animation.

// SummaryShimmerStyle selects how a shimmer span is rendered.
type SummaryShimmerStyle int

const (
	// SummaryShimmerPlain is the reduced-motion rendering: no styling at all.
	SummaryShimmerPlain SummaryShimmerStyle = iota
	// SummaryShimmerDim is used when the terminal palette is unknown.
	SummaryShimmerDim
	// SummaryShimmerColor carries a truecolor foreground for the moving band.
	SummaryShimmerColor
)

// SummaryShimmerSpan is one styled grapheme of an animating status header.
type SummaryShimmerSpan struct {
	Text       string
	Style      SummaryShimmerStyle
	Foreground RGB
}

// SummaryShimmer renders the status header with the two-second brightness
// sweep. Reduced motion returns the text unstyled; an unknown palette or a
// non-truecolor terminal returns it dim.
func SummaryShimmer(text string, elapsed time.Duration, motion MotionMode) []SummaryShimmerSpan {
	if motion == MotionReduced {
		return []SummaryShimmerSpan{{Text: text, Style: SummaryShimmerPlain}}
	}
	fg, bg, ok := defaultTerminalColorsRGB()
	if !ok || DetectStdoutColorLevel() != ColorTrue {
		return []SummaryShimmerSpan{{Text: text, Style: SummaryShimmerDim}}
	}
	return summaryShimmerTruecolor(text, elapsed, fg, bg)
}

// SummaryShimmerSpansAt renders the shimmer for an explicit palette, which the
// terminal-agnostic core and tests use to exercise the animated path.
func SummaryShimmerSpansAt(text string, elapsed time.Duration, motion MotionMode, fg RGB, bg RGB, trueColor bool) []SummaryShimmerSpan {
	if motion == MotionReduced {
		return []SummaryShimmerSpan{{Text: text, Style: SummaryShimmerPlain}}
	}
	if !trueColor {
		return []SummaryShimmerSpan{{Text: text, Style: SummaryShimmerDim}}
	}
	return summaryShimmerTruecolor(text, elapsed, fg, bg)
}

func summaryShimmerTruecolor(text string, elapsed time.Duration, fg RGB, bg RGB) []SummaryShimmerSpan {
	width := float64(DisplayWidth(text))
	halfWidth := math.Max(width*0.1, 3.0)
	position := math.Mod(elapsed.Seconds(), 2.0)/2.0*(width+2.0*halfWidth) - halfWidth
	spans := []SummaryShimmerSpan{}
	column := 0.0
	graphemes := uniseg.NewGraphemes(text)
	for graphemes.Next() {
		glyph := graphemes.Str()
		glyphWidth := float64(graphemes.Width())
		center := column + glyphWidth/2.0
		column += glyphWidth
		distance := math.Min(math.Abs(center-position)/halfWidth, 1.0)
		intensity := 0.5 * (1.0 + math.Cos(math.Pi*distance))
		alpha := 0.5 + 0.5*intensity
		spans = append(spans, SummaryShimmerSpan{
			Text:       glyph,
			Style:      SummaryShimmerColor,
			Foreground: BlendRGB(fg, bg, alpha),
		})
	}
	return spans
}
