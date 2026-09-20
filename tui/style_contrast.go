package tui

import (
	"math"
	"sync"
)

// Rust parity: codex-rs/tui/src/style/contrast.rs - resolve informative
// foregrounds against their actual background before palette reduction. A
// missing background sample preserves the requested palette colour; unknown
// colour capabilities and user-defined ANSI palettes retain the terminal's
// default foreground.

// MinTextContrast is the WCAG contrast ratio Rust requires of informative text
// (Rust MIN_TEXT_CONTRAST).
const MinTextContrast = 4.5

// maxCachedForegrounds bounds the resolver cache (Rust MAX_CACHED_FOREGROUNDS).
const maxCachedForegrounds = 512

// minSelectionFillRatio keeps a selection fill from disappearing into a
// similarly coloured canvas (Rust's 1.25 threshold).
const minSelectionFillRatio = 1.25

type contrastForegroundKey struct {
	preferred  RGB
	background RGB
	hasBG      bool
	level      StdoutColorLevel
}

var (
	contrastForegroundMu    sync.Mutex
	contrastForegroundCache = map[contrastForegroundKey]TerminalColor{}
)

// ContrastForeground resolves a preferred foreground against the background it
// is painted on, at the terminal's colour depth (Rust's `contrast::foreground`).
// Without a background sample the requested colour is reduced to the palette as
// before.
func ContrastForeground(preferred RGB, background *RGB, level StdoutColorLevel) TerminalColor {
	key := contrastForegroundKey{preferred: preferred, level: level}
	if background != nil {
		key.background = *background
		key.hasBG = true
	}
	contrastForegroundMu.Lock()
	if cached, ok := contrastForegroundCache[key]; ok {
		contrastForegroundMu.Unlock()
		return cached
	}
	contrastForegroundMu.Unlock()

	color := resolveContrastForeground(preferred, key.background, key.hasBG, level)

	contrastForegroundMu.Lock()
	if len(contrastForegroundCache) >= maxCachedForegrounds {
		contrastForegroundCache = map[contrastForegroundKey]TerminalColor{}
	}
	contrastForegroundCache[key] = color
	contrastForegroundMu.Unlock()
	return color
}

func resolveContrastForeground(preferred RGB, background RGB, hasBackground bool, level StdoutColorLevel) TerminalColor {
	if !hasBackground {
		return BestColorForLevel(preferred, level)
	}
	switch level {
	case ColorTrue:
		if ContrastRatio(preferred, background) >= MinTextContrast {
			return TerminalColor{RGB: &preferred}
		}
		black := RGB{}
		white := RGB{R: 255, G: 255, B: 255}
		endpoint := white
		if ContrastRatio(black, background) >= ContrastRatio(white, background) {
			endpoint = black
		}
		// Bounded search preserves as much of the requested hue as the surface
		// allows.
		for step := 1; step <= 255; step++ {
			candidate := BlendRGB(endpoint, preferred, float64(step)/255.0)
			if ContrastRatio(candidate, background) >= MinTextContrast {
				return TerminalColor{RGB: &candidate}
			}
		}
		return TerminalColor{RGB: &endpoint}
	case ColorANSI256:
		palette := xterm256Palette()
		bestIndex := -1
		bestDistance := math.MaxFloat64
		for index := 16; index < len(palette); index++ {
			color := palette[index]
			if ContrastRatio(color, background) < MinTextContrast {
				continue
			}
			distance := PerceptualDistance(color, preferred)
			if distance < bestDistance {
				bestDistance = distance
				bestIndex = index
			}
		}
		if bestIndex < 0 {
			return TerminalColor{}
		}
		index := uint8(bestIndex)
		return TerminalColor{Index: &index}
	default:
		return TerminalColor{}
	}
}

// ContrastRatio reports the WCAG contrast ratio of two colours (Rust's `ratio`).
func ContrastRatio(a RGB, b RGB) float64 {
	first := relativeLuminance(a)
	second := relativeLuminance(b)
	return (math.Max(first, second) + 0.05) / (math.Min(first, second) + 0.05)
}

func relativeLuminance(rgb RGB) float64 {
	linear := func(channel uint8) float64 {
		value := float64(channel) / 255.0
		if value <= 0.04045 {
			return value / 12.92
		}
		return math.Pow((value+0.055)/1.055, 2.4)
	}
	return 0.2126*linear(rgb.R) + 0.7152*linear(rgb.G) + 0.0722*linear(rgb.B)
}

// SelectionStyleFor resolves Rust's `selection_style`: the ChatGPT blue fill for
// the given surface, contrast-corrected, or the terminal-defaults fallback when
// the palette is unknown.
func SelectionStyleFor(background *RGB, level StdoutColorLevel) StyleSpec {
	fallback := StyleSpec{Reversed: true, Bold: true}
	if background == nil {
		return fallback
	}
	preferred, alternate := ChatGPTBlue200, ChatGPTBlue100
	if IsLight(*background) {
		preferred, alternate = ChatGPTBlue100, ChatGPTBlue200
	}
	fill := BestColorForLevel(preferred, level)
	fillRGB, ok := terminalColorRGB(fill)
	if !ok {
		return fallback
	}
	// A subtle fill is intentional, but it must not disappear into a similarly
	// blue canvas.
	if ContrastRatio(fillRGB, *background) < minSelectionFillRatio {
		alternateResolved := BestColorForLevel(alternate, level)
		if alternateRGB, ok := terminalColorRGB(alternateResolved); ok &&
			ContrastRatio(alternateRGB, *background) > ContrastRatio(fillRGB, *background) {
			fill = alternateResolved
			fillRGB = alternateRGB
		}
	}
	foreground := ContrastForeground(RGB{R: 0, G: 0, B: 46}, &fillRGB, level)
	return StyleSpec{Foreground: foreground, Background: fill, Bold: true}
}

// ActiveTabStyleFor resolves Rust's `picker_style::active_tab_style`: the filled
// active tab, with a readable foreground on the fill, or the terminal-defaults
// fallback (bold and underlined) when the palette is unknown.
func ActiveTabStyleFor(background *RGB, level StdoutColorLevel) StyleSpec {
	fallback := StyleSpec{Underlined: true, Bold: true}
	if background == nil {
		return fallback
	}
	fillHint := RGB{R: 76, G: 76, B: 76}
	if IsLight(*background) {
		fillHint = RGB{R: 220, G: 220, B: 220}
	}
	fill := BestColorForLevel(fillHint, level)
	fillRGB, ok := terminalColorRGB(fill)
	if !ok {
		return fallback
	}
	// Rust resolves the terminal's default foreground against the fill; an
	// unknown default foreground keeps the terminal's own colour (Color::Reset).
	foreground := TerminalColor{}
	if fg, _, known := defaultTerminalColorsRGB(); known {
		foreground = ContrastForeground(fg, &fillRGB, level)
	}
	return StyleSpec{
		Foreground: foreground,
		Background: fill,
		Bold:       true,
	}
}

// terminalColorRGB resolves a terminal colour to its concrete RGB value: an
// explicit colour, or an indexed colour at or above the 16 ANSI entries (Rust's
// `resolve` in contrast.rs and `background_rgb` in style.rs).
func terminalColorRGB(color TerminalColor) (RGB, bool) {
	switch {
	case color.RGB != nil:
		return *color.RGB, true
	case color.Index != nil && *color.Index >= 16:
		palette := xterm256Palette()
		if int(*color.Index) < len(palette) {
			return palette[*color.Index], true
		}
	}
	return RGB{}, false
}
