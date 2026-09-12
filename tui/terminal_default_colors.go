package tui

import "sync"

// Rust parity: codex-rs/tui/src/terminal_palette.rs (#43921). The palette cache
// records the terminal's default foreground/background colors once. Rust fills
// it from the startup probe or an OSC 10/11 query; Go's TUI does not run a
// startup probe, so callers may supply the probe's DefaultColors and otherwise
// the cache reports the colors as unavailable (rendering falls back to dim
// text, matching Rust's unknown-palette branch).

var (
	terminalDefaultColorsMu        sync.Mutex
	terminalDefaultColorsAttempted bool
	terminalDefaultColorsValue     *DefaultColors
)

// defaultTerminalColors reports the cached terminal default colors. Like Rust's
// palette cache, the first lookup records the result so a later probe result is
// only observed when it is installed through SetDefaultColorsFromStartupProbe
// or the test seam.
func defaultTerminalColors() (DefaultColors, bool) {
	terminalDefaultColorsMu.Lock()
	defer terminalDefaultColorsMu.Unlock()
	if !terminalDefaultColorsAttempted {
		terminalDefaultColorsAttempted = true
		terminalDefaultColorsValue = nil
	}
	if terminalDefaultColorsValue == nil {
		return DefaultColors{}, false
	}
	return *terminalDefaultColorsValue, true
}

// SetDefaultColorsFromStartupProbe records the startup probe's terminal default
// colors (Rust set_default_colors_from_startup_probe). A nil value marks the
// lookup as attempted-but-unavailable.
func SetDefaultColorsFromStartupProbe(colors *DefaultColors) {
	terminalDefaultColorsMu.Lock()
	defer terminalDefaultColorsMu.Unlock()
	terminalDefaultColorsAttempted = true
	if colors == nil {
		terminalDefaultColorsValue = nil
		return
	}
	cloned := *colors
	terminalDefaultColorsValue = &cloned
}

// SetDefaultTerminalColorsForTest installs colors for a test and returns a
// restore function, mirroring Rust's with_test_default_colors seam.
func SetDefaultTerminalColorsForTest(colors *DefaultColors) func() {
	terminalDefaultColorsMu.Lock()
	previousAttempted := terminalDefaultColorsAttempted
	previousValue := terminalDefaultColorsValue
	terminalDefaultColorsMu.Unlock()
	SetDefaultColorsFromStartupProbe(colors)
	return func() {
		terminalDefaultColorsMu.Lock()
		defer terminalDefaultColorsMu.Unlock()
		terminalDefaultColorsAttempted = previousAttempted
		terminalDefaultColorsValue = previousValue
	}
}

// defaultTerminalColorsRGB reports the default colors as RGB for shading.
func defaultTerminalColorsRGB() (RGB, RGB, bool) {
	colors, ok := defaultTerminalColors()
	if !ok {
		return RGB{}, RGB{}, false
	}
	return RGB{R: colors.FG.R, G: colors.FG.G, B: colors.FG.B},
		RGB{R: colors.BG.R, G: colors.BG.G, B: colors.BG.B}, true
}
