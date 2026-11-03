package tui

import (
	"math"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

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

// colorLevelOverride pins the color level for tests so rendering can be
// exercised at a chosen palette depth without probing the host terminal. It
// mirrors Rust's test-only promotion of TEST_DEFAULT_COLORS to truecolor in
// terminal_palette.rs.
var colorLevelOverride atomic.Pointer[StdoutColorLevel]

// SetStdoutColorLevelForTest pins DetectStdoutColorLevel and returns a restore
// function.
func SetStdoutColorLevelForTest(level StdoutColorLevel) func() {
	previous := colorLevelOverride.Load()
	pinned := level
	colorLevelOverride.Store(&pinned)
	return func() {
		colorLevelOverride.Store(previous)
	}
}

// DetectStdoutColorLevel reports the terminal's color depth, cached for the
// lifetime of the process so a render pass uses one stable palette.
//
// Rust parity: codex-rs/tui/src/terminal_palette.rs stdout_color_level plus the
// Windows Terminal truecolor promotion. Windows Terminal fully supports 24-bit
// color but often advertises only ANSI-16 (no COLORTERM), so WT_SESSION is
// promoted to truecolor unless FORCE_COLOR is present.
func DetectStdoutColorLevel() StdoutColorLevel {
	if level := colorLevelOverride.Load(); level != nil {
		return *level
	}
	detectedColorLevelOnce.Do(func() {
		detectedColorLevel = detectStdoutColorLevel()
	})
	return detectedColorLevel
}

// envLookup reads an environment variable and reports whether it is present.
// Rust's supports-color distinguishes "absent" from "present but empty":
// NO_COLOR="" disables colour while NO_COLOR="0" does not. os.LookupEnv keeps
// that distinction observable, os.Getenv does not.
type envLookup func(key string) (string, bool)

func osEnvLookup(key string) (string, bool) { return os.LookupEnv(key) }

func detectStdoutColorLevel() StdoutColorLevel {
	return detectStdoutColorLevelFor(osEnvLookup, stdoutIsTerminal())
}

// detectStdoutColorLevelFor is detectStdoutColorLevel with the environment and
// the tty probe injected, so colour detection can be exercised without touching
// (or depending on) the host environment.
func detectStdoutColorLevelFor(lookup envLookup, stdoutIsTTY bool) StdoutColorLevel {
	stdoutLevel := supportsColorLevel(lookup, stdoutIsTTY)
	_, hasWTSession := lookup("WT_SESSION")
	// Rust gates the promotion on FORCE_COLOR being *present*, not on it holding
	// a particular value: FORCE_COLOR="" is an override too.
	_, hasForceColorOverride := lookup("FORCE_COLOR")
	if hasWTSession && !hasForceColorOverride {
		return ColorTrue
	}
	if stdoutLevel == ColorANSI16 && isWindowsTerminal(lookup) && !hasForceColorOverride {
		return ColorTrue
	}
	return stdoutLevel
}

// isWindowsTerminal approximates codex_terminal_detection's WindowsTerminal
// name. TERM_PROGRAM is the only signal available without probing the console.
func isWindowsTerminal(lookup envLookup) bool {
	termProgram, ok := lookup("TERM_PROGRAM")
	return ok && strings.EqualFold(termProgram, "Windows_Terminal")
}

// supportsColorLevel ports supports-color 3.0.2 `supports_color()`, the crate
// Rust's stdout_color_level() delegates to (codex-rs/Cargo.toml pins 3.0.2).
func supportsColorLevel(lookup envLookup, stdoutIsTTY bool) StdoutColorLevel {
	if level, ok := forcedColorLevel(lookup); ok {
		return level
	}
	// FORCE_COLOR=0 / CLICOLOR_FORCE=0 fall through to here: they clear the
	// forced level without being a "no colour" request.
	if noColorRequested(lookup) || isDumbTerminal(lookup) ||
		!(stdoutIsTTY || ignoreIsTerminal(lookup)) {
		return ColorUnknown
	}
	if value, ok := lookup("COLORTERM"); ok && (value == "truecolor" || value == "24bit") {
		return ColorTrue
	}
	if value, ok := lookup("TERM"); ok &&
		(strings.HasSuffix(value, "direct") || strings.HasSuffix(value, "truecolor")) {
		return ColorTrue
	}
	if termProgram, ok := lookup("TERM_PROGRAM"); ok && termProgram == "iTerm.app" {
		return ColorTrue
	}
	if termProgram, ok := lookup("TERM_PROGRAM"); ok && termProgram == "Apple_Terminal" {
		return ColorANSI256
	}
	if value, ok := lookup("TERM"); ok &&
		(strings.HasSuffix(value, "256") || strings.HasSuffix(value, "256color")) {
		return ColorANSI256
	}
	if _, ok := lookup("COLORTERM"); ok {
		return ColorANSI16
	}
	if ansiColorForTerm(lookup) {
		return ColorANSI16
	}
	if value, ok := lookup("CLICOLOR"); ok && value != "0" {
		return ColorANSI16
	}
	// Known delta: supports-color also consults the is-ci crate at this point.
	// Go has no equivalent dependency and the branch is unreachable without a
	// tty, so it is omitted rather than guessed (see hermetic_alignment.md).
	return ColorUnknown
}

// noColorRequested ports supports-color `env_no_color()`: NO_COLOR disables
// colour whenever it is present, with the literal "0" as the sole exception.
func noColorRequested(lookup envLookup) bool {
	value, ok := lookup("NO_COLOR")
	return ok && value != "0"
}

func isDumbTerminal(lookup envLookup) bool {
	term, ok := lookup("TERM")
	return ok && term == "dumb"
}

func ignoreIsTerminal(lookup envLookup) bool {
	value, ok := lookup("IGNORE_IS_TERMINAL")
	return ok && value != "0"
}

// ansiColorForTerm ports supports-color `check_ansi_color()`, which differs by
// platform: Windows assumes ANSI support when TERM is absent.
func ansiColorForTerm(lookup envLookup) bool {
	term, ok := lookup("TERM")
	if runtime.GOOS == "windows" {
		if !ok {
			return true
		}
		return term != "dumb" && term != "cygwin"
	}
	if !ok {
		return false
	}
	return term != "dumb"
}

// forcedColorLevel ports supports-color `env_force_color()`. Anything that is
// not "true"/"false"/a number forces ANSI-16, and levels are capped at 3.
func forcedColorLevel(lookup envLookup) (StdoutColorLevel, bool) {
	level := 0
	if value, ok := lookup("FORCE_COLOR"); ok {
		switch value {
		case "true", "":
			level = 1
		case "false":
			level = 0
		default:
			parsed, err := strconv.ParseUint(value, 10, 8)
			if err != nil {
				parsed = 1
			}
			if parsed > 3 {
				parsed = 3
			}
			level = int(parsed)
		}
	} else if value, ok := lookup("CLICOLOR_FORCE"); ok && value != "0" {
		level = 1
	}
	switch level {
	case 0:
		return ColorUnknown, false
	case 1:
		return ColorANSI16, true
	case 2:
		return ColorANSI256, true
	default:
		return ColorTrue, true
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
