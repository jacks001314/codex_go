package tui

import "math"

// Rust #50209 ("Make transcript mouse scroll speed configurable"): the
// transcript treats `tui.mouse_scroll_speed` as a finite positive multiplier
// over a single row per wheel event. Before #50209 the wheel always moved a
// fixed three rows; the multiplier is applied instead, and fractional values
// accumulate across events (Rust `TranscriptView::pending_mouse_scroll`).

// MouseScrollSpeedDefault mirrors the Rust default of one row per wheel event
// (`TranscriptView::default().mouse_scroll_speed == 1.0`, #50209). A configured
// value of `3.0` restores the pre-#50209 speed.
const MouseScrollSpeedDefault = 1.0

// MouseScrollSpeedValid mirrors the `tui.mouse_scroll_speed` validation in Rust
// `codex-rs/config/src/tui_mouse_scroll.rs` (#50209): the multiplier must be a
// finite positive number.
func MouseScrollSpeedValid(speed float64) bool {
	return !math.IsNaN(speed) && !math.IsInf(speed, 0) && speed > 0
}

// NormalizeMouseScrollSpeed resolves a configured multiplier to the live value.
// An absent (`None`) or unusable setting falls back to the default, so the
// transcript is never left with a non-finite or non-positive speed.
func NormalizeMouseScrollSpeed(speed *float64) float64 {
	if speed == nil || !MouseScrollSpeedValid(*speed) {
		return MouseScrollSpeedDefault
	}
	return *speed
}

// MouseScrollAccumulator mirrors the `mouse_scroll_speed` +
// `pending_mouse_scroll` pair of Rust `TranscriptView` (#50209): wheel input
// accumulates fractional rows, and the remainder is discarded when the
// direction reverses rather than cancelling the pending movement.
type MouseScrollAccumulator struct {
	speed   float64
	pending float64
}

// NewMouseScrollAccumulator builds an accumulator for one configured speed. A
// nil speed means the default (Rust `unwrap_or(1.0)`).
func NewMouseScrollAccumulator(speed *float64) MouseScrollAccumulator {
	return MouseScrollAccumulator{speed: NormalizeMouseScrollSpeed(speed)}
}

// SetSpeed applies a reloaded `tui.mouse_scroll_speed`. The pending fractional
// remainder is kept, matching Rust, where only `mouse_scroll_speed` is replaced.
func (a *MouseScrollAccumulator) SetSpeed(speed float64) {
	if a == nil {
		return
	}
	a.speed = NormalizeMouseScrollSpeed(&speed)
}

// Speed reports the live multiplier.
func (a *MouseScrollAccumulator) Speed() float64 {
	if a == nil {
		return MouseScrollSpeedDefault
	}
	return NormalizeMouseScrollSpeed(&a.speed)
}

// PendingRows reports the fractional movement retained for the next wheel event
// in the same direction.
func (a *MouseScrollAccumulator) PendingRows() float64 {
	if a == nil {
		return 0
	}
	return a.pending
}

// Rows consumes one wheel event and returns the whole rows to scroll. direction
// is negative for the wheel up (toward earlier output) and positive for the
// wheel down, matching Rust's `TranscriptView::handle_mouse` ScrollUp/ScrollDown
// arm (#50209).
func (a *MouseScrollAccumulator) Rows(direction float64) int {
	if a == nil || direction == 0 {
		return 0
	}
	sign := 1.0
	if direction < 0 {
		sign = -1
	}
	// Rust compares `f64::signum()` against the event direction, so a pending
	// movement in the opposite direction is dropped instead of cancelled.
	if a.pendingSign() != sign {
		a.pending = 0
	}
	a.pending += sign * a.Speed()
	// Decimal speeds can land just short of a whole row after repeated addition.
	if rounded := math.Round(a.pending); rounded != 0 && math.Abs(a.pending-rounded) < 1e-9 {
		a.pending = rounded
	}
	// Rust's `as isize` truncates toward zero and `fract()` keeps the signed
	// remainder for the next event.
	rows := int(a.pending)
	a.pending -= math.Trunc(a.pending)
	if rows == 0 {
		return 0
	}
	return rows
}

func (a *MouseScrollAccumulator) pendingSign() float64 {
	if a.pending < 0 {
		return -1
	}
	return 1
}
