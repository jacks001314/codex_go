package tui

import (
	"math"
	"os"
	"runtime"
	"strings"
	"sync"

	"github.com/muesli/termenv"
)

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

var detectedColorLevel StdoutColorLevel
var detectedColorLevelOnce sync.Once

// DetectStdoutColorLevel reports the terminal's color depth, cached for the
// lifetime of the process so a render pass uses one stable palette.
//
// Rust parity: codex-rs/tui/src/terminal_palette.rs stdout_color_level plus the
// Windows Terminal truecolor promotion. Windows Terminal fully supports 24-bit
// color but often advertises only ANSI-16 (no COLORTERM), so WT_SESSION is
// promoted to truecolor unless FORCE_COLOR is set.
func DetectStdoutColorLevel() StdoutColorLevel {
	detectedColorLevelOnce.Do(func() {
		detectedColorLevel = detectStdoutColorLevel()
	})
	return detectedColorLevel
}

func detectStdoutColorLevel() StdoutColorLevel {
	if runtime.GOOS == "windows" {
		// Windows Terminal advertises only ANSI-16 to some probes even though it
		// renders truecolor correctly; promote by WT_SESSION or TERM_PROGRAM
		// unless the user forced a color level (Rust: effective_stdout_color_level).
		hasWindowsTerminal := os.Getenv("WT_SESSION") != "" ||
			strings.EqualFold(os.Getenv("TERM_PROGRAM"), "Windows_Terminal")
		if hasWindowsTerminal && os.Getenv("FORCE_COLOR") == "" {
			return ColorTrue
		}
	}
	switch termenv.ColorProfile() {
	case termenv.TrueColor:
		return ColorTrue
	case termenv.ANSI256:
		return ColorANSI256
	case termenv.ANSI:
		return ColorANSI16
	default:
		return ColorUnknown
	}
}

// diffColorLevel returns the effective color depth for diff/syntax rendering.
// Unknown terminals fall back to ANSI-16 (foreground-only) so truecolor escape
// sequences are never emitted to a terminal that cannot handle them.
func diffColorLevel() StdoutColorLevel {
	level := DetectStdoutColorLevel()
	if level == ColorUnknown {
		return ColorANSI16
	}
	return level
}

// TermenvProfileForLevel maps a detected color depth to the termenv profile
// used by glamour / markdown rendering so text styling stays within the
// terminal's actual color support.
func TermenvProfileForLevel(level StdoutColorLevel) termenv.Profile {
	switch level {
	case ColorTrue:
		return termenv.TrueColor
	case ColorANSI256:
		return termenv.ANSI256
	default:
		return termenv.ANSI
	}
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
