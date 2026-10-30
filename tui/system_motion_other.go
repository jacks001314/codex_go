//go:build !windows && !darwin && !linux

package tui

// platformSystemMotion reports that the host preference is unavailable on
// platforms without a supported system motion source.
func platformSystemMotion() (MotionMode, bool) {
	return MotionAnimated, false
}
