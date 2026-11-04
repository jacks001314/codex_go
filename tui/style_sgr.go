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

// HistoryPromptSGR renders Rust's `style::history_prompt_style` surface for
// submitted prompts: a 16% (dark) or 2% (light) blend toward the terminal
// background. A terminal that does not answer the default-color probe gets the
// xterm grays with 8.4:1 contrast (Rust #59257); ANSI-16 terminals keep their
// own foreground and background.
func HistoryPromptSGR(level StdoutColorLevel) string {
	_, terminalBG, known := defaultTerminalColorsRGB()
	if !known {
		switch level {
		case ColorTrue:
			return "\x1b[38;2;238;238;238;48;2;68;68;68m"
		case ColorANSI256:
			return "\x1b[38;5;255;48;5;238m"
		default:
			return ""
		}
	}
	codes := sgrColorCodes(bestHistoryPromptFill(terminalBG, level), false, level)
	if len(codes) == 0 {
		return ""
	}
	return "\x1b[" + strings.Join(codes, ";") + "m"
}

// HistoryPromptTextSGR restores the fallback prompt foreground after a span
// reset, such as the voice marker. A resolved terminal palette keeps the
// terminal's default foreground.
func HistoryPromptTextSGR(level StdoutColorLevel) string {
	_, _, known := defaultTerminalColorsRGB()
	if known {
		return ""
	}
	switch level {
	case ColorTrue:
		return "\x1b[38;2;238;238;238m"
	case ColorANSI256:
		return "\x1b[38;5;255m"
	default:
		return ""
	}
}

// HistoryPromptAccentAllowed reports whether user-message element accents may
// be painted on the submitted-prompt surface. With a resolved palette the
// accent follows the fill; without one, the ANSI-safe fallback keeps the
// terminal's default foreground for ANSI-16 and paints the same accent when a
// truecolor/256-color profile is available (Rust #59257).
func HistoryPromptAccentAllowed(level StdoutColorLevel) bool {
	return level != ColorUnknown
}

func bestHistoryPromptFill(terminalBG RGB, level StdoutColorLevel) TerminalColor {
	top := RGB{R: 255, G: 255, B: 255}
	alpha := 0.16
	if IsLight(terminalBG) {
		top = RGB{}
		alpha = 0.02
	}
	return BestColorForLevel(BlendRGB(top, terminalBG, alpha), level)
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

// BackgroundSGR renders a background-only SGR prefix. Rust paints block fills
// with `Style::default().bg(color)`, which emits no foreground code, so a fill
// must not also reset the terminal's default foreground. An unresolved colour
// or a terminal without colour support yields no prefix.
func BackgroundSGR(color TerminalColor, level StdoutColorLevel) string {
	if level <= ColorUnknown || (color.RGB == nil && color.Index == nil) {
		return ""
	}
	codes := sgrColorCodes(color, false, level)
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
