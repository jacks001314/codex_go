package tui

import "time"

// Rust parity: codex-rs/tui/src/motion.rs.

var processStart = time.Now()

type MotionMode int

const (
	MotionAnimated MotionMode = iota
	MotionReduced
)

type ReducedMotionIndicator int

const (
	ReducedMotionHidden ReducedMotionIndicator = iota
	ReducedMotionStaticBullet
)

func MotionModeFromAnimationsEnabled(enabled bool) MotionMode {
	if enabled {
		return MotionAnimated
	}
	return MotionReduced
}

func ActivityIndicator(startTime *time.Time, mode MotionMode, reduced ReducedMotionIndicator, trueColor bool, now time.Time) (string, bool) {
	switch mode {
	case MotionReduced:
		if reduced == ReducedMotionHidden {
			return "", false
		}
		return "\u2022", true
	default:
		return AnimatedActivityIndicator(startTime, trueColor, now), true
	}
}

func AnimatedActivityIndicator(startTime *time.Time, trueColor bool, now time.Time) string {
	if trueColor {
		return "\u2022"
	}
	var elapsed time.Duration
	if startTime != nil {
		elapsed = now.Sub(*startTime)
	}
	if (elapsed.Milliseconds()/600)%2 == 0 {
		return "\u2022"
	}
	return "\u25e6"
}

func ShimmerText(text string, mode MotionMode) []ShimmerSpan {
	switch mode {
	case MotionReduced:
		if text == "" {
			return nil
		}
		return []ShimmerSpan{{Text: text, Intensity: 0}}
	default:
		return ShimmerSpans(text)
	}
}
