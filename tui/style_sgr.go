package tui

import (
	"strconv"
	"strings"
)

// SGR rendering for the resolved style specs the string-based TUI layers paint
// (Rust paints the same specs through ratatui::Style).

// SelectionSGR renders Rust's `style::selection_style` for the terminal's
// current background and colour depth: the resolved ChatGPT-blue fill with a
// readable foreground, or the bold reversed terminal defaults when the palette
// is unknown.
func SelectionSGR(level StdoutColorLevel) string {
	background, ok := DefaultBackgroundRGB()
	if !ok {
		return SGRForStyle(StyleSpec{Bold: true, Reversed: true}, level)
	}
	return SGRForStyle(SelectionStyleFor(&background, level), level)
}

// ActiveTabSGR renders Rust's `picker_style::active_tab_style`: the filled tab
// with a readable foreground, or the bold underlined terminal defaults when the
// palette is unknown.
func ActiveTabSGR(level StdoutColorLevel) string {
	background, ok := DefaultBackgroundRGB()
	if !ok {
		return SGRForStyle(StyleSpec{Bold: true, Underlined: true}, level)
	}
	return SGRForStyle(ActiveTabStyleFor(&background, level), level)
}

// DefaultBackgroundRGB reports the terminal's default background colour when the
// palette is known (Rust's terminal_palette::default_bg).
func DefaultBackgroundRGB() (RGB, bool) {
	_, background, ok := defaultTerminalColorsRGB()
	return background, ok
}

// SGRForStyle renders one style spec as an SGR prefix. An unresolved colour
// falls back to the terminal default (39/49), matching Rust's Color::Reset.
func SGRForStyle(spec StyleSpec, level StdoutColorLevel) string {
	codes := []string{}
	if spec.Bold {
		codes = append(codes, "1")
	}
	if spec.Dim {
		codes = append(codes, "2")
	}
	if spec.Underlined {
		codes = append(codes, "4")
	}
	if spec.Reversed {
		codes = append(codes, "7")
	}
	codes = append(codes, sgrColorCodes(spec.Foreground, true, level)...)
	codes = append(codes, sgrColorCodes(spec.Background, false, level)...)
	if len(codes) == 0 {
		return ""
	}
	return "\x1b[" + strings.Join(codes, ";") + "m"
}

func sgrColorCodes(color TerminalColor, foreground bool, level StdoutColorLevel) []string {
	parameter := "48"
	if foreground {
		parameter = "38"
	}
	switch {
	case color.RGB != nil:
		if level != ColorTrue {
			resolved := BestColorForLevel(*color.RGB, level)
			return sgrColorCodes(resolved, foreground, level)
		}
		return []string{parameter, "2", strconv.Itoa(int(color.RGB.R)), strconv.Itoa(int(color.RGB.G)), strconv.Itoa(int(color.RGB.B))}
	case color.Index != nil:
		return []string{parameter, "5", strconv.Itoa(int(*color.Index))}
	default:
		if foreground {
			return []string{"39"}
		}
		return []string{"49"}
	}
}
