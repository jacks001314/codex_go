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

// Rust parity: codex-rs/tui/src/motion.rs::loading_glyph /
// loading_glyph_with_delay (#50112). Rust moves the voice strip's connection
// spinner here so one helper owns the frames and the 100 ms cadence, and lets
// the helper schedule the next animation frame through the TUI's
// FrameRequester. Go's renderer has no frame requester - the host repaints on
// the voice meter tick (`chatwidget.VoiceMicrophoneMeterInterval`) - so the
// helpers report the delay until the next frame instead of scheduling it.

// LoadingGlyphFrameDuration is the shared loading-glyph cadence (Rust's
// `motion::loading_glyph_with_delay` FRAME_DURATION).
const LoadingGlyphFrameDuration = 100 * time.Millisecond

// LoadingGlyphReducedMotion is the static glyph shown when animations are off
// (Rust's reduced-motion arm of `loading_glyph_with_delay`).
const LoadingGlyphReducedMotion = "\u25cc"

// LoadingGlyphFrames are the braille frames a loading glyph cycles through.
var LoadingGlyphFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// LoadingGlyph returns the loading frame for the elapsed time. Rust's version
// also schedules the next frame; Go's callers repaint on their own tick.
func LoadingGlyph(startedAt time.Time, mode MotionMode, now time.Time) string {
	glyph, _ := loadingGlyphWithDelay(startedAt, 0, mode, now)
	return glyph
}

// loadingGlyphWithDelay renders nothing during the caller's delay, or a static
// glyph in reduced motion, and reports how long the caller should wait before
// the next frame. Rust schedules that frame inside the helper through the
// FrameRequester instead of returning the delay.
func loadingGlyphWithDelay(startedAt time.Time, delay time.Duration, mode MotionMode, now time.Time) (string, time.Duration) {
	if mode == MotionReduced {
		return LoadingGlyphReducedMotion, 0
	}
	elapsed := now.Sub(startedAt)
	if elapsed < 0 {
		// Rust's `Instant::elapsed` never runs backwards; a Go caller can still
		// hand the helper a clock that is earlier than the start time.
		elapsed = 0
	}
	if elapsed < delay {
		return "", delay - elapsed
	}
	frame := int(elapsed/LoadingGlyphFrameDuration) % len(LoadingGlyphFrames)
	return LoadingGlyphFrames[frame], LoadingGlyphFrameDuration
}
