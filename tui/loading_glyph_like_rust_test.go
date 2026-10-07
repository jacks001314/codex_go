package tui

import (
	"testing"
	"time"
)

// TestLoadingGlyphLikeRust pins the centralized loading glyph from Rust #50112
// (codex-rs/tui/src/motion.rs::loading_glyph / loading_glyph_with_delay, moved
// out of codex-rs/tui/src/bottom_pane/voice_strip.rs): the shared braille table
// advances every 100 ms, wraps, and reduced motion returns the static glyph at
// any elapsed time. Rust compares against the same table in its voice-strip
// rendering.
func TestLoadingGlyphLikeRust(t *testing.T) {
	started := time.Unix(0, 0)
	if len(LoadingGlyphFrames) != 10 {
		t.Fatalf("shared frame table has %d frames, want Rust's 10", len(LoadingGlyphFrames))
	}
	for _, want := range []struct {
		elapsed time.Duration
		frame   int
	}{
		{elapsed: 0, frame: 0},
		{elapsed: 99 * time.Millisecond, frame: 0},
		{elapsed: 100 * time.Millisecond, frame: 1},
		{elapsed: 150 * time.Millisecond, frame: 1},
		{elapsed: 999 * time.Millisecond, frame: 9},
		{elapsed: 1000 * time.Millisecond, frame: 0},
		{elapsed: 2100 * time.Millisecond, frame: 1},
	} {
		got := LoadingGlyph(started, MotionAnimated, started.Add(want.elapsed))
		if expected := LoadingGlyphFrames[want.frame]; got != expected {
			t.Fatalf("glyph at %v = %q, want %q", want.elapsed, got, expected)
		}
	}
	if got := LoadingGlyph(started, MotionReduced, started.Add(time.Second)); got != LoadingGlyphReducedMotion {
		t.Fatalf("reduced-motion glyph = %q, want %q", got, LoadingGlyphReducedMotion)
	}
	// Rust's `Instant::elapsed` cannot run backwards; a Go clock that does must
	// still produce the first frame instead of an empty marker.
	if got := LoadingGlyph(started, MotionAnimated, started.Add(-time.Second)); got != LoadingGlyphFrames[0] {
		t.Fatalf("glyph for a backwards clock = %q, want %q", got, LoadingGlyphFrames[0])
	}
}

// TestLoadingGlyphSchedulesNextFrameLikeRust pins the frame scheduling Rust's
// helper performs through its FrameRequester: nothing renders until the
// caller's delay expires, and the helper reports the 100 ms cadence to the
// caller that owns the repaint (the host's voice meter tick in Go).
func TestLoadingGlyphSchedulesNextFrameLikeRust(t *testing.T) {
	started := time.Unix(0, 0)
	const delay = 500 * time.Millisecond
	for _, want := range []struct {
		elapsed  time.Duration
		glyph    string
		nextWait time.Duration
	}{
		{elapsed: 0, glyph: "", nextWait: delay},
		{elapsed: 100 * time.Millisecond, glyph: "", nextWait: 400 * time.Millisecond},
		{elapsed: 499 * time.Millisecond, glyph: "", nextWait: time.Millisecond},
		// Rust derives the frame from the total elapsed time, not from the time
		// since the delay expired, so the first visible frame is the one that
		// matches `elapsed` itself.
		{elapsed: delay, glyph: LoadingGlyphFrames[5], nextWait: LoadingGlyphFrameDuration},
		{elapsed: 750 * time.Millisecond, glyph: LoadingGlyphFrames[7], nextWait: LoadingGlyphFrameDuration},
	} {
		glyph, nextWait := loadingGlyphWithDelay(started, delay, MotionAnimated, started.Add(want.elapsed))
		if glyph != want.glyph || nextWait != want.nextWait {
			t.Fatalf("delayed glyph at %v = (%q, %v), want (%q, %v)", want.elapsed, glyph, nextWait, want.glyph, want.nextWait)
		}
	}
	// Reduced motion ignores the delay, exactly like Rust's early return.
	glyph, nextWait := loadingGlyphWithDelay(started, delay, MotionReduced, started)
	if glyph != LoadingGlyphReducedMotion || nextWait != 0 {
		t.Fatalf("delayed reduced-motion glyph = (%q, %v), want (%q, 0)", glyph, nextWait, LoadingGlyphReducedMotion)
	}
}
