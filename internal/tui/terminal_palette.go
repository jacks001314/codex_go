package tui

import "math"

// Rust parity: codex-rs/tui/src/terminal_palette.rs.

type StdoutColorLevel int

const (
	ColorUnknown StdoutColorLevel = iota
	ColorANSI16
	ColorANSI256
	ColorTrue
)

type RGB struct {
	R uint8
	G uint8
	B uint8
}

type TerminalColor struct {
	RGB   *RGB
	Index *uint8
}

func BestColorForLevel(target RGB, level StdoutColorLevel) TerminalColor {
	switch level {
	case ColorTrue:
		return TerminalColor{RGB: &target}
	case ColorANSI256:
		index := ClosestANSI256(target)
		return TerminalColor{Index: &index}
	default:
		return TerminalColor{}
	}
}

func ClosestANSI256(target RGB) uint8 {
	bestIndex := uint8(0)
	bestDistance := math.MaxFloat64
	for index, color := range xterm256Palette() {
		distance := colorDistance(target, color)
		if distance < bestDistance {
			bestDistance = distance
			bestIndex = uint8(index)
		}
	}
	return bestIndex
}

func colorDistance(a RGB, b RGB) float64 {
	dr := float64(int(a.R) - int(b.R))
	dg := float64(int(a.G) - int(b.G))
	db := float64(int(a.B) - int(b.B))
	return dr*dr*0.30 + dg*dg*0.59 + db*db*0.11
}

func xterm256Palette() []RGB {
	colors := make([]RGB, 0, 256)
	base := []RGB{
		{0, 0, 0}, {128, 0, 0}, {0, 128, 0}, {128, 128, 0},
		{0, 0, 128}, {128, 0, 128}, {0, 128, 128}, {192, 192, 192},
		{128, 128, 128}, {255, 0, 0}, {0, 255, 0}, {255, 255, 0},
		{0, 0, 255}, {255, 0, 255}, {0, 255, 255}, {255, 255, 255},
	}
	colors = append(colors, base...)
	steps := []uint8{0, 95, 135, 175, 215, 255}
	for _, r := range steps {
		for _, g := range steps {
			for _, b := range steps {
				colors = append(colors, RGB{r, g, b})
			}
		}
	}
	for i := 0; i < 24; i++ {
		value := uint8(8 + i*10)
		colors = append(colors, RGB{value, value, value})
	}
	return colors
}
