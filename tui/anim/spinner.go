package anim

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"strings"
	"sync"

	bubbletea "github.com/charmbracelet/bubbletea"
)

// SpinnerMode controls the visual style of a Spinner.
type SpinnerMode int

const (
	// SpinnerDots cycles through ".  ", ".. ", "...", "   " for a "Thinking..."-style label.
	SpinnerDots SpinnerMode = iota
	// SpinnerScramble displays a "scrambled" text effect where characters randomly cycle
	// through a charset before resolving, creating a decryption-like animation.
	SpinnerScramble
	// SpinnerPulse shows a pulsing indicator with varying intensity.
	SpinnerPulse
)

// SpinnerConfig describes the appearance and behavior of a Spinner.
type SpinnerConfig struct {
	// Label is the static text shown before the animated portion.
	Label string
	// Mode sets the animation style.
	Mode SpinnerMode
	// ScrambleSet is the set of characters to use in scramble mode.
	// Defaults to "0123456789abcdefABCDEF~!@#$...+".
	ScrambleSet string
	// Colors is a slice of 2 or more hex color strings for gradient cycling.
	// For example: []string{"#FF6B6B", "#4ECDC4"}.
	Colors []string
	// Suffix is optional text shown after the animated portion (e.g., elapsed time).
	Suffix string
}

// Spinner is an animated terminal component that renders a cycling indicator.
// It uses a global frame cache keyed by config hash for zero-allocation reuse.
type Spinner struct {
	config SpinnerConfig
	engine *Engine
	tick   int
	frames []string
}

// =============================================================================
// Frame cache
// =============================================================================

var (
	frameCacheMu sync.RWMutex
	frameCache   = map[string][]string{}
)

func configHash(cfg SpinnerConfig) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%d|%s|%v|%s", cfg.Label, cfg.Mode, cfg.ScrambleSet, cfg.Colors, cfg.Suffix)
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func defaultScrambleSet() string {
	return "0123456789abcdefABCDEF~!@#$%^&*()-_=+[]{}|;:,.<>?/"
}

// =============================================================================
// Constructor
// =============================================================================

// NewSpinner creates a Spinner with the given configuration and engine.
func NewSpinner(cfg SpinnerConfig, engine *Engine) *Spinner {
	if cfg.ScrambleSet == "" {
		cfg.ScrambleSet = defaultScrambleSet()
	}
	if engine == nil {
		engine = NewEngine(0)
	}
	s := &Spinner{config: cfg, engine: engine}
	s.frames = s.getOrBuildFrames()
	return s
}

func (s *Spinner) getOrBuildFrames() []string {
	key := configHash(s.config)

	frameCacheMu.RLock()
	if frames, ok := frameCache[key]; ok {
		frameCacheMu.RUnlock()
		return frames
	}
	frameCacheMu.RUnlock()

	frames := s.buildFrames()

	frameCacheMu.Lock()
	frameCache[key] = frames
	frameCacheMu.Unlock()

	return frames
}

func (s *Spinner) buildFrames() []string {
	switch s.config.Mode {
	case SpinnerScramble:
		return s.buildScrambleFrames()
	case SpinnerPulse:
		return s.buildPulseFrames()
	default:
		return s.buildDotsFrames()
	}
}

// =============================================================================
// Frame builders
// =============================================================================

func (s *Spinner) buildDotsFrames() []string {
	// 4-frame cycle: ".  ", ".. ", "...", "   "
	return []string{
		s.renderFrame(".  "),
		s.renderFrame(".. "),
		s.renderFrame("..."),
		s.renderFrame("   "),
	}
}

func (s *Spinner) buildScrambleFrames() []string {
	charset := []rune(s.config.ScrambleSet)
	target := s.config.Label
	targetRunes := []rune(target)
	n := len(targetRunes)

	// Generate frames: each character resolves one-by-one with stagger
	const framesPerChar = 4
	totalFrames := n*framesPerChar + framesPerChar

	frames := make([]string, totalFrames)
	for f := 0; f < totalFrames; f++ {
		var display []rune
		for i := 0; i < n; i++ {
			// Character i resolves around frame i*framesPerChar
			resolveAt := i * framesPerChar
			if f >= resolveAt {
				display = append(display, targetRunes[i])
			} else {
				// Scramble: pick pseudo-random char from charset
				idx := (f*7 + i*13) % len(charset)
				display = append(display, charset[idx])
			}
		}
		frames[f] = s.renderFrame(string(display))
	}
	return frames
}

func (s *Spinner) buildPulseFrames() []string {
	const numFrames = 20
	frames := make([]string, numFrames)
	for f := 0; f < numFrames; f++ {
		// Pulse intensity oscillates with sine wave
		t := float64(f) / float64(numFrames)
		intensity := (math.Sin(t*2*math.Pi-math.Pi/2) + 1) / 2 // 0..1

		blocks := int(intensity * 5) // 0..5 blocks
		indicator := strings.Repeat("█", blocks) + strings.Repeat("░", 5-blocks)
		frames[f] = s.renderFrame(indicator)
	}
	return frames
}

func (s *Spinner) renderFrame(animated string) string {
	var b strings.Builder
	if s.config.Label != "" {
		b.WriteString(s.config.Label)
	}
	b.WriteString(animated)
	if s.config.Suffix != "" {
		b.WriteString(" ")
		b.WriteString(s.config.Suffix)
	}
	return b.String()
}

// =============================================================================
// Bubble Tea integration
// =============================================================================

// Update processes a Bubble Tea message and returns a command for the next tick.
// Returns nil if the message is not a TickMsg.
func (s *Spinner) Update(msg bubbletea.Msg) bubbletea.Cmd {
	tickMsg, ok := msg.(TickMsg)
	if !ok {
		return nil
	}
	s.tick = tickMsg.Tick
	return s.engine.Advance()
}

// View returns the current frame string.
func (s *Spinner) View() string {
	if len(s.frames) == 0 {
		return s.config.Label
	}
	idx := s.tick % len(s.frames)
	return s.frames[idx]
}

// InitCmd returns the Bubble Tea command to start the animation.
func (s *Spinner) InitCmd() bubbletea.Cmd {
	return s.engine.TickCmd()
}

// Engine returns the underlying animation engine.
func (s *Spinner) Engine() *Engine { return s.engine }
