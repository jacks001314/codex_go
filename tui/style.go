package tui

// Rust parity: codex-rs/tui/src/style.rs.

var lightBGAccentRGB = RGB{R: 0, G: 95, B: 135}

const tableSeparatorFGAlpha = 0.20

type StyleSpec struct {
	Foreground TerminalColor
	Background TerminalColor
	Bold       bool
	Dim        bool
}

func AccentStyleFor(terminalBG *RGB, colorLevel StdoutColorLevel) StyleSpec {
	if terminalBG != nil && IsLight(*terminalBG) {
		return StyleSpec{Foreground: BestColorForLevel(lightBGAccentRGB, colorLevel), Bold: true}
	}
	return StyleSpec{Foreground: BestColorForLevel(RGB{R: 0, G: 255, B: 255}, colorLevel), Bold: true}
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
