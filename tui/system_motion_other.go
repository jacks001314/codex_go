//go:build !windows

package tui

// platformSystemMotion reports that the host preference is unavailable. Rust
// reads NSWorkspace on macOS and the XDG desktop portal with a 250ms bound on
// Linux; the Go TUI has no cgo/D-Bus dependency, so those hosts preserve the
// configured `tui.animations` behavior instead.
func platformSystemMotion() (MotionMode, bool) {
	return MotionAnimated, false
}
