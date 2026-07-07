package tui

import (
	"math"
	"time"
)

// Rust parity: codex-rs/tui/src/shimmer.rs.

type ShimmerSpan struct {
	Text      string
	Intensity float64
}

func ShimmerSpans(text string) []ShimmerSpan {
	return ShimmerSpansAt(text, time.Since(processStart))
}

func ShimmerSpansAt(text string, elapsed time.Duration) []ShimmerSpan {
	chars := []rune(text)
	if len(chars) == 0 {
		return nil
	}
	spans := make([]ShimmerSpan, 0, len(chars))
	for i, ch := range chars {
		spans = append(spans, ShimmerSpan{
			Text:      string(ch),
			Intensity: ShimmerIntensity(i, len(chars), elapsed),
		})
	}
	return spans
}

func ShimmerIntensity(index int, textLen int, elapsed time.Duration) float64 {
	if textLen <= 0 {
		return 0
	}
	padding := 10
	period := textLen + padding*2
	sweepSeconds := 2.0
	elapsedSeconds := math.Mod(elapsed.Seconds(), sweepSeconds)
	pos := int(elapsedSeconds / sweepSeconds * float64(period))
	dist := math.Abs(float64(index + padding - pos))
	bandHalfWidth := 5.0
	if dist > bandHalfWidth {
		return 0
	}
	x := math.Pi * (dist / bandHalfWidth)
	return 0.5 * (1.0 + math.Cos(x))
}
