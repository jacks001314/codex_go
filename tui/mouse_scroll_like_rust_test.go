package tui

import (
	"math"
	"testing"
)

// Rust #50209 (4dd51f4a5f, "Make transcript mouse scroll speed configurable"),
// test `mouse_scroll_speed_scales_rows_and_accumulates_fractional_movement`
// (codex-rs/tui/src/transcript_view/input_tests.rs). The Rust table gives the
// cumulative rows the view must have moved after each wheel event; the first
// ceil(len/2) events scroll up and the rest scroll down.
func TestMouseScrollSpeedScalesRowsAndAccumulatesFractionalMovementLikeRust(t *testing.T) {
	speed05 := 0.5
	speed15 := 1.5
	speed30 := 3.0
	cases := []struct {
		name   string
		speed  *float64
		deltas []int
	}{
		{name: "default", speed: nil, deltas: []int{-1, 1}},
		{name: "half", speed: &speed05, deltas: []int{0, -1, 0, 0, 1}},
		{name: "one_and_a_half", speed: &speed15, deltas: []int{-1, -2, 1, 2}},
		{name: "three", speed: &speed30, deltas: []int{-3, 3}},
	}
	for _, tc := range cases {
		acc := NewMouseScrollAccumulator(tc.speed)
		upEvents := (len(tc.deltas) + 1) / 2
		moved := 0
		want := 0
		for i, delta := range tc.deltas {
			direction := 1.0
			if i < upEvents {
				direction = -1
			}
			moved += acc.Rows(direction)
			want += delta
			if moved != want {
				t.Fatalf("%s: after event %d moved %d rows, want %d", tc.name, i+1, moved, want)
			}
		}
	}
}

// Rust #50209, test `decimal_mouse_scroll_speeds_preserve_whole_rows`
// (codex-rs/tui/src/transcript_view/input_tests.rs): a decimal speed keeps the
// whole rows it has earned across repeated events in one direction, and the
// remainder is discarded when the direction reverses.
func TestDecimalMouseScrollSpeedsPreserveWholeRowsLikeRust(t *testing.T) {
	for _, speed := range []float64{0.1, 0.3} {
		acc := NewMouseScrollAccumulator(&speed)
		for _, direction := range []float64{-1, 1} {
			moved := 0
			for event := 1; event <= 100; event++ {
				moved += acc.Rows(direction)
				// The Rust test adds `f64::from(event) * speed).floor() as isize * direction`
				// to the start row, so an up phase moves toward earlier rows.
				want := int(direction) * int(math.Floor(float64(event)*speed))
				if moved != want {
					t.Fatalf("speed %v, event %d: moved %d rows, want %d", speed, event, moved, want)
				}
			}
			if moved == 0 {
				t.Fatalf("speed %v: 100 events moved no rows", speed)
			}
		}
	}
}

// Rust #50209, `TranscriptView::default().mouse_scroll_speed` is 1.0: one row
// per wheel event, down from the fixed three rows the wheel used before.
func TestMouseScrollSpeedDefaultsToOneRowPerEventLikeRust(t *testing.T) {
	acc := NewMouseScrollAccumulator(nil)
	if got := acc.Speed(); got != MouseScrollSpeedDefault {
		t.Fatalf("default speed = %v, want %v", got, MouseScrollSpeedDefault)
	}
	if got := acc.Rows(1); got != 1 {
		t.Fatalf("default wheel-down rows = %d, want 1", got)
	}
	acc = NewMouseScrollAccumulator(nil)
	if got := acc.Rows(-1); got != -1 {
		t.Fatalf("default wheel-up rows = %d, want -1", got)
	}
}

// Rust #50209, `mouse_scroll_speed_rejects_nonpositive_and_nonfinite_values`
// (codex-rs/config/src/types_tests.rs): the multiplier must be finite and
// positive; Rust rejects the value at parse time, and the live TUI must never
// adopt a speed it cannot use.
func TestMouseScrollSpeedRejectsNonpositiveAndNonfiniteLikeRust(t *testing.T) {
	for _, value := range []float64{0, -0.5, math.NaN(), math.Inf(1), math.Inf(-1)} {
		if MouseScrollSpeedValid(value) {
			t.Fatalf("speed %v reported valid", value)
		}
		if got := NormalizeMouseScrollSpeed(&value); got != MouseScrollSpeedDefault {
			t.Fatalf("speed %v normalized to %v, want default %v", value, got, MouseScrollSpeedDefault)
		}
	}
	// Rust #50209's accepted multipliers, `mouse_scroll_speed_accepts_integer_and_fractional_multipliers`.
	for _, value := range []float64{1, 0.5, 3.0, 1.5} {
		if !MouseScrollSpeedValid(value) {
			t.Fatalf("speed %v reported invalid", value)
		}
		v := value
		if got := NormalizeMouseScrollSpeed(&v); got != value {
			t.Fatalf("speed %v normalized to %v", value, got)
		}
	}
	if got := NormalizeMouseScrollSpeed(nil); got != MouseScrollSpeedDefault {
		t.Fatalf("absent speed normalized to %v, want default %v", got, MouseScrollSpeedDefault)
	}
}

// Rust #50209: a fractional wheel movement is kept for the next event in the
// same direction, and reversed input drops the remainder rather than cancelling
// it (Rust `f64::signum() != direction`).
func TestMouseScrollAccumulatorKeepsFractionalRemainderLikeRust(t *testing.T) {
	speed := 0.5
	acc := NewMouseScrollAccumulator(&speed)
	if got := acc.Rows(1); got != 0 {
		t.Fatalf("first half-row event moved %d rows, want 0", got)
	}
	if got := acc.PendingRows(); math.Abs(got-0.5) > 1e-9 {
		t.Fatalf("pending rows = %v, want 0.5", got)
	}
	if got := acc.Rows(1); got != 1 {
		t.Fatalf("second half-row event moved %d rows, want 1", got)
	}
	if got := acc.PendingRows(); got != 0 {
		t.Fatalf("pending rows after a whole row = %v, want 0", got)
	}
	// The remainder is discarded, not cancelled, when the direction reverses.
	if got := acc.Rows(1); got != 0 {
		t.Fatalf("third half-row event moved %d rows, want 0", got)
	}
	if got := acc.Rows(-1); got != 0 {
		t.Fatalf("reversed event moved %d rows, want 0", got)
	}
	if got := acc.PendingRows(); math.Abs(got+0.5) > 1e-9 {
		t.Fatalf("pending rows after reversal = %v, want -0.5", got)
	}
}
