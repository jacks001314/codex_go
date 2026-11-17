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
	defaultFG, defaultBG, known := defaultTerminalColorsRGB()
	if !ok {
		// Reset (or an indexed ANSI entry): the terminal's default foreground,
		// or the terminal's own colour when it is unknown.
		if !known {
			return TerminalColor{}
		}
		resolved = defaultFG
	}
	if background == nil && known {
		// Rust's `background_rgb(None)` falls back to the terminal's default
		// background, so a nil surface still yields a contrast-corrected colour.
		background = &defaultBG
	}
	return ContrastForeground(resolved, background, level)
}

func UserMessageBackground(terminalBG RGB, colorLevel StdoutColorLevel) TerminalColor {
	return BestColorForLevel(UserMessageBackgroundRGB(terminalBG), colorLevel)
}

// UserMessageBackgroundRGB is Rust's `user_message_bg_rgb`: the terminal
// background shaded onto itself, so the surface stays one step away from the
// canvas.
func UserMessageBackgroundRGB(terminalBG RGB) RGB {
	return ShadeRGB(terminalBG, terminalBG)
}

// ShadeRGB lifts a surface toward white by 12% on dark terminals and toward
// black by 4% on light ones (Rust's `style::shade_rgb`).
func ShadeRGB(surface RGB, terminalBG RGB) RGB {
	top := RGB{R: 255, G: 255, B: 255}
	alpha := 0.12
	if IsLight(terminalBG) {
		top = RGB{}
		alpha = 0.04
	}
	return BlendRGB(top, surface, alpha)
}

// SecondaryTextStyle is Rust's `secondary_text_style`: the terminal foreground
// blended 60% toward the background, contrast-corrected against the canvas and
// never dimmed.
func SecondaryTextStyle(level StdoutColorLevel) StyleSpec {
	preferred := TerminalColor{}
	if foreground, background, known := defaultTerminalColorsRGB(); known {
		blended := BlendRGB(foreground, background, 0.6)
		preferred = TerminalColor{RGB: &blended}
	}
	return StyleSpec{Foreground: ReadableColorOn(preferred, nil, level)}
}

// SecondarySurfaceStyle resolves the quiet chip a tab strip paints before any
// hover or selection (Rust sidebar_tab_strip.rs `rest`): secondary text patched
// onto the user-message surface, with the foreground re-resolved readable on
// that fill.
func SecondarySurfaceStyle(level StdoutColorLevel) StyleSpec {
	rest := SecondaryTextStyle(level)
	terminalBG, known := DefaultBackgroundRGB()
	if !known {
		return rest
	}
	fill := UserMessageBackground(terminalBG, level)
	rest.Background = fill
	if fillRGB, ok := terminalColorRGB(fill); ok {
		rest.Foreground = ReadableColorOn(rest.Foreground, &fillRGB, level)
	}
	return rest
}

// QuietWorkspaceStyle is Rust's `quiet_workspace_style`: a focusable surface
// pulled toward the terminal background, or the dim terminal default when the
// palette is unknown.
func QuietWorkspaceStyle(level StdoutColorLevel) StyleSpec {
	foreground, terminalBG, known := defaultTerminalColorsRGB()
	fill := BestColorForLevel(BlendRGB(UserMessageBackgroundRGB(terminalBG), terminalBG, 0.40), level)
	fillRGB, ok := terminalColorRGB(fill)
	if !known || !ok {
		return StyleSpec{Dim: true}
	}
	blended := BestColorForLevel(BlendRGB(foreground, terminalBG, 0.66), level)
	return StyleSpec{
		Foreground: ReadableColorOn(blended, &fillRGB, level),
		Background: fill,
	}
}

// HoverStyleFor is Rust's `style::hover_style`: shading the surface twice
// toward the terminal background strengthens the fill under the pointer. A
// terminal without a known palette, or a fill that would not move, falls back
// to an underline so the target still reads as live.
func HoverStyleFor(spec StyleSpec, level StdoutColorLevel) StyleSpec {
	terminalBG, known := DefaultBackgroundRGB()
	return hoverStyleForBackground(spec, terminalBG, known, level)
}

func hoverStyleForBackground(spec StyleSpec, terminalBG RGB, known bool, level StdoutColorLevel) StyleSpec {
	if !known {
		spec.Underlined = true
		return spec
	}
	surface, ok := styleSurfaceRGB(spec.Background, terminalBG)
	if !ok {
		spec.Underlined = true
		return spec
	}
	fill := BestColorForLevel(ShadeRGB(ShadeRGB(surface, terminalBG), terminalBG), level)
	fillRGB, ok := terminalColorRGB(fill)
	if !ok || sameTerminalColor(fill, spec.Background) {
		spec.Underlined = true
		return spec
	}
	spec.Background = fill
	spec.Foreground = ReadableColorOn(spec.Foreground, &fillRGB, level)
	return spec
}

// styleSurfaceRGB resolves a style background the way Rust's `background_rgb`
// does: a concrete colour, the xterm entry for an indexed colour at or above
// 16, and the terminal's default background for an unset or reset background.
func styleSurfaceRGB(background TerminalColor, terminalBG RGB) (RGB, bool) {
	if background.RGB == nil && background.Index == nil {
		return terminalBG, true
	}
	return terminalColorRGB(background)
}

// sameTerminalColor reports whether two resolved colours are the same paint.
func sameTerminalColor(a TerminalColor, b TerminalColor) bool {
	switch {
	case a.RGB != nil && b.RGB != nil:
		return *a.RGB == *b.RGB
	case a.Index != nil && b.Index != nil:
		return *a.Index == *b.Index
	}
	return false
}

func ProposedPlanBackground(terminalBG RGB, colorLevel StdoutColorLevel) TerminalColor {
	return UserMessageBackground(terminalBG, colorLevel)
}

// ProposedPlanSurface resolves the shaded proposed-plan background against the
// terminal's default background (Rust #54315 plans.rs `plan_style`). ok is
// false when the terminal's default colors are unknown, in which case the plan
// renders unshaded, like Rust's no-palette branch.
func ProposedPlanSurface(level StdoutColorLevel) (TerminalColor, RGB, bool) {
	_, terminalBG, known := defaultTerminalColorsRGB()
	if !known {
		return TerminalColor{}, RGB{}, false
	}
	surface := ProposedPlanBackground(terminalBG, level)
	rgb, ok := terminalColorRGB(surface)
	if !ok {
		return TerminalColor{}, RGB{}, false
	}
	return surface, rgb, true
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
