package tui

// Rust parity: codex-rs/tui/src/style.rs.

// Shared TUI colours (Rust's ChatGPT blues and the light-background accent).
var (
	// ChatGPTBlue100 is #A4CDFB, the selection fill on light backgrounds.
	ChatGPTBlue100 = RGB{R: 164, G: 205, B: 251}
	// ChatGPTBlue200 is #63A8F8.
	ChatGPTBlue200 = RGB{R: 99, G: 168, B: 248}
	// UIAccent is the shared accent for picker selection and transcript emphasis.
	UIAccent = ChatGPTBlue200
	// LightBGAccentRGB is the accent on a light terminal background
	// (Rust LIGHT_BG_ACCENT_RGB = (28, 100, 200)).
	lightBGAccentRGB = RGB{R: 28, G: 100, B: 200}
)

const tableSeparatorFGAlpha = 0.20

type StyleSpec struct {
	Foreground TerminalColor
	Background TerminalColor
	Bold       bool
	Dim        bool
	// Underlined and Reversed carry the modifiers the picker fallbacks use
	// (Rust's active-tab and selection styles).
	Underlined bool
	Reversed   bool
}

func AccentStyleFor(terminalBG *RGB, colorLevel StdoutColorLevel) StyleSpec {
	return StyleSpec{Foreground: AccentColorFor(terminalBG, colorLevel), Bold: true}
}

// AccentColorFor resolves the shared accent against the terminal background
// (Rust's `accent_color_for`): the light-background accent on light themes, the
// ChatGPT blue otherwise, contrast-corrected for the surface.
func AccentColorFor(terminalBG *RGB, colorLevel StdoutColorLevel) TerminalColor {
	preferred := UIAccent
	if terminalBG != nil && IsLight(*terminalBG) {
		preferred = lightBGAccentRGB
	}
	return ContrastForeground(preferred, terminalBG, colorLevel)
}

// ReadableColorOn keeps theme-derived text readable on its painted surface
// (Rust's `readable_color_on`): an explicit colour is resolved against the
// background, `Reset` uses the terminal's default foreground, and any other
// colour is returned unchanged.
func ReadableColorOn(preferred TerminalColor, background *RGB, level StdoutColorLevel) TerminalColor {
	if preferred.Index != nil && *preferred.Index < 16 {
		return preferred
	}
	resolved, ok := terminalColorRGB(preferred)
	if !ok {
		// Reset (or an indexed ANSI entry): the terminal's default foreground,
		// or the terminal's own colour when it is unknown.
		fg, _, known := defaultTerminalColorsRGB()
		if !known {
			return TerminalColor{}
		}
		resolved = fg
	}
	return ContrastForeground(resolved, background, level)
}

func UserMessageBackground(terminalBG RGB, colorLevel StdoutColorLevel) TerminalColor {
	top := RGB{R: 255, G: 255, B: 255}
	alpha := 0.12
	if IsLight(terminalBG) {
		top = RGB{}
		alpha = 0.04
	}
	return BestColorForLevel(BlendRGB(top, terminalBG, alpha), colorLevel)
}

func ProposedPlanBackground(terminalBG RGB, colorLevel StdoutColorLevel) TerminalColor {
	return UserMessageBackground(terminalBG, colorLevel)
}

func TableSeparatorStyleFor(terminalFG *RGB, terminalBG *RGB, colorLevel StdoutColorLevel) StyleSpec {
	if terminalFG == nil || terminalBG == nil {
		return StyleSpec{Dim: true}
	}
	separator := BlendRGB(*terminalFG, *terminalBG, tableSeparatorFGAlpha)
	switch colorLevel {
	case ColorTrue, ColorANSI256:
		return StyleSpec{Foreground: BestColorForLevel(separator, colorLevel)}
	default:
		return StyleSpec{Dim: true}
	}
}
