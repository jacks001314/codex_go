package tui

import (
	"strings"
	"sync"
)

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

// parseMacOSReduceMotion maps `defaults read com.apple.universalaccess
// reduceMotion` to a motion mode. Any other value means no preference.
func parseMacOSReduceMotion(output string) (MotionMode, bool) {
	switch strings.TrimSpace(output) {
	case "1":
		return MotionReduced, true
	case "0":
		return MotionAnimated, true
	default:
		return MotionAnimated, false
	}
}

// parsePortalReducedMotion maps the XDG desktop portal's `ReadOne` reply for
// `org.freedesktop.appearance.reduced-motion` (for example `(<<uint32 1>>,)`)
// to a motion mode. The portal defines values other than 1 as no preference.
func parsePortalReducedMotion(output string) (MotionMode, bool) {
	index := strings.Index(output, "uint32")
	if index < 0 {
		return MotionAnimated, false
	}
	digits := strings.Builder{}
	for _, r := range output[index+len("uint32"):] {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
			continue
		}
		if digits.Len() > 0 {
			break
		}
	}
	switch digits.String() {
	case "1":
		return MotionReduced, true
	case "0":
		return MotionAnimated, true
	default:
		return MotionAnimated, false
	}
}
