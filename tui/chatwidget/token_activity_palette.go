package chatwidget

import (
	"fmt"

	codextui "codex_go/tui"
)

// Rust parity: codex-rs/tui/src/analytics/activity_chart/palette.rs.
//
// The palette adapts theme colors to the active terminal color level while
// keeping the chart-specific glyph policy local to the token activity
// renderer. In low-color terminals empty and active cells are distinguished by
// a width-matched filled/hollow glyph pair; at higher color depths the grid
// uses one glyph and lets color carry the intensity (GitHub-style).

// TokenActivityPalette stores the terminal-specific styles and glyph strategy
// for token activity cells.
type TokenActivityPalette struct {
	styles    [5]string
	barStyle  string
	usesColor bool
}

// CurrentTokenActivityPalette builds the palette for the terminal's effective
// color level (Rust #46897): activity charts honor the same terminal
// adjustments as the rest of the TUI, including the Windows Terminal truecolor
// promotion and the FORCE_COLOR override.
func CurrentTokenActivityPalette(themeID string) TokenActivityPalette {
	colors, ok := codextui.DefaultTerminalColors()
	if !ok {
		return fallbackTokenActivityPalette()
	}
	return tokenActivityPaletteFromParts(
		codextui.RGB{R: colors.FG.R, G: colors.FG.G, B: colors.FG.B},
		codextui.RGB{R: colors.BG.R, G: colors.BG.G, B: colors.BG.B},
		codextui.DetectStdoutColorLevel(),
		codextui.ThemeAccentColor(themeID),
	)
}

// tokenActivityPaletteFromParts is the testable constructor (Rust
// TokenActivityPalette::from_parts).
func tokenActivityPaletteFromParts(fg codextui.RGB, bg codextui.RGB, level codextui.StdoutColorLevel, accentHex string) TokenActivityPalette {
	if level == codextui.ColorANSI16 || level == codextui.ColorUnknown {
		return fallbackTokenActivityPalette()
	}
	r, g, b, ok := codextui.ParseThemeAccentHex(accentHex)
	if !ok {
		return fallbackTokenActivityPalette()
	}
	accent := codextui.RGB{R: r, G: g, B: b}
	emptyAlpha := 0.14
	if codextui.IsLight(bg) {
		emptyAlpha = 0.18
	}
	alphas := [5]float64{emptyAlpha, 0.22, 0.42, 0.68, 1.00}
	var palette TokenActivityPalette
	for index := range alphas {
		source := accent
		if index == 0 {
			source = fg
		}
		palette.styles[index] = tokenActivityColorSGR(codextui.BestColorForLevel(codextui.BlendRGB(source, bg, alphas[index]), level))
	}
	palette.barStyle = tokenActivityColorSGR(codextui.BestColorForLevel(codextui.BlendRGB(accent, bg, 0.78), level))
	palette.usesColor = true
	return palette
}

// fallbackTokenActivityPalette mirrors Rust TokenActivityPalette::fallback: the
// empty cell is dim, every active cell keeps the terminal's default color, and
// the glyph pair separates empty from active cells.
func fallbackTokenActivityPalette() TokenActivityPalette {
	return TokenActivityPalette{styles: [5]string{"\x1b[2m"}, usesColor: false}
}

// UsesColor reports whether the palette renders a color gradient, so the grid
// can encode intensity purely by color with a single glyph.
func (p TokenActivityPalette) UsesColor() bool {
	return p.usesColor
}

// StyleForLevel returns the SGR prefix for a daily cell at one of the five
// activity levels.
func (p TokenActivityPalette) StyleForLevel(level int) string {
	if level < 0 {
		level = 0
	}
	if level > 4 {
		level = 4
	}
	return p.styles[level]
}

// StyleForBarLevel returns the SGR prefix for a bar cell: empty columns keep
// the empty style and every filled level shares the bar color.
func (p TokenActivityPalette) StyleForBarLevel(level int) string {
	if level == 0 {
		return p.StyleForLevel(0)
	}
	return p.barStyle
}

// Glyph returns the cell glyph for a level. Daily charts render every visible
// cell with the same square when color carries the intensity, and fall back to
// the hollow square for empty cells at low color depths. Bar views use full
// blocks for filled height and spaces for empty height.
func (p TokenActivityPalette) Glyph(view TokenActivityView, level int) string {
	if view != TokenActivityDaily {
		if level == 0 {
			return " "
		}
		return tokenActivityBarCellGlyph
	}
	if p.usesColor || level > 0 {
		return tokenActivityActiveCellGlyph
	}
	return tokenActivityEmptyCellGlyph
}

func tokenActivityColorSGR(color codextui.TerminalColor) string {
	if color.RGB != nil {
		return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", color.RGB.R, color.RGB.G, color.RGB.B)
	}
	if color.Index != nil {
		return fmt.Sprintf("\x1b[38;5;%dm", *color.Index)
	}
	return ""
}
