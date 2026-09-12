package tui

import "sync"

// Rust parity: codex-rs/tui/src/system_motion.rs (#44666).
//
// The host's accessibility motion preference is read once per process and
// cached. Detection never changes persisted configuration: an unavailable
// preference preserves the configured behavior (an explicit request for less
// motion suppresses effects governed by `tui.animations`). Changing the OS
// preference requires restarting the TUI.

var (
	systemMotionOnce   sync.Once
	systemMotionMode   MotionMode
	detectSystemMotion = platformSystemMotion
)

// InitializeSystemMotion records the host motion preference for this process.
// Repeated calls are ignored.
func InitializeSystemMotion() {
	SystemMotionMode()
}

// SystemMotionMode returns the launch-time host motion preference, defaulting
// to MotionAnimated when detection is unavailable.
func SystemMotionMode() MotionMode {
	systemMotionOnce.Do(func() {
		mode, ok := detectSystemMotion()
		if !ok {
			mode = MotionAnimated
		}
		systemMotionMode = mode
	})
	return systemMotionMode
}

// EffectiveAnimations applies the host motion preference to a configured
// `tui.animations` value without changing the saved configuration.
func EffectiveAnimations(configured bool) bool {
	return configured && SystemMotionMode() == MotionAnimated
}

// resetSystemMotionForTest clears the cached preference so tests can exercise
// each detection outcome.
func resetSystemMotionForTest() {
	systemMotionOnce = sync.Once{}
	systemMotionMode = MotionAnimated
}
