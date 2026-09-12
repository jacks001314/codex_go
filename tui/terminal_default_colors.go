package tui

import "sync"

// Rust parity: codex-rs/tui/src/terminal_palette.rs (#43921). The palette cache
// records the terminal's default foreground/background colors once. Rust fills
// it from the startup probe or, failing that, an OSC 10/11 query falling back
// to the native console palette. Go's TUI does not run the startup probe, so
// callers may supply the probe's DefaultColors; otherwise the first lookup
// queries the platform source (the Windows console color table) and reports the
// colors as unavailable when there is none (rendering falls back to dim text,
// matching Rust's unknown-palette branch).

var (
	terminalDefaultColorsMu        sync.Mutex
	terminalDefaultColorsAttempted bool
	terminalDefaultColorsValue     *DefaultColors
)

// DefaultTerminalColors reports the terminal's default foreground/background
// colors, querying the platform source once when the cache is unset (Rust
// terminal_palette::default_colors).
func DefaultTerminalColors() (DefaultColors, bool) {
	return defaultTerminalColors()
}

// defaultTerminalColors reports the cached terminal default colors. Like Rust's
// palette cache, the first lookup records the result so a later probe result is
// only observed when it is installed through SetDefaultColorsFromStartupProbe
// or the test seam.
func defaultTerminalColors() (DefaultColors, bool) {
	terminalDefaultColorsMu.Lock()
	defer terminalDefaultColorsMu.Unlock()
	if !terminalDefaultColorsAttempted {
		terminalDefaultColorsAttempted = true
		// Mirrors Rust Cache::get_or_init_with(query_default_colors): the first
		// lookup records the platform result for the process lifetime.
		if colors, ok := platformTerminalDefaultColors(); ok {
			value := colors
			terminalDefaultColorsValue = &value
		}
	}
	if terminalDefaultColorsValue == nil {
		return DefaultColors{}, false
	}
	return *terminalDefaultColorsValue, true
}

// decodeConsoleDefaultColors maps a Windows console attributes word and color
// table to the terminal's default colors, mirroring Rust
// decode_console_default_colors. The low nibble selects the foreground palette
// entry and the high nibble the background entry.
func decodeConsoleDefaultColors(attributes uint16, colorTable [16]uint32) DefaultColors {
	return DefaultColors{
		FG: decodeColorRef(colorTable[attributes&0x0f]),
		BG: decodeColorRef(colorTable[(attributes>>4)&0x0f]),
	}
}

func decodeColorRef(colorRef uint32) RGBColor {
	return RGBColor{
		R: uint8(colorRef & 0xff),
		G: uint8((colorRef >> 8) & 0xff),
		B: uint8((colorRef >> 16) & 0xff),
	}
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
