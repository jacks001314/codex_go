//go:build !windows

package tui

// platformTerminalDefaultColors reports the terminal default colors on hosts
// without a native console palette. Rust queries OSC 10/11 over a duplicated
// terminal handle here; Go's TUI does not own that read path, so the palette
// stays unavailable and callers use the dim fallback.
func platformTerminalDefaultColors() (DefaultColors, bool) {
	return DefaultColors{}, false
}
