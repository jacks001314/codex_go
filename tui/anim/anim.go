// Package anim provides a lightweight animation engine for terminal spinners
// and progress indicators. It integrates with the Bubble Tea framework via
// tea.Tick-based frame messages.
package anim

import (
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"
)

// TickMsg is sent on each animation frame tick.
type TickMsg struct{ Tick int }

// Engine drives per-frame animation updates at a fixed rate.
// It is safe to share a single Engine across multiple animating components.
type Engine struct {
	tick   int
	fps    int
	paused bool
}

// NewEngine creates an animation Engine with the given FPS (defaults to 20 if fps <= 0).
func NewEngine(fps int) *Engine {
	if fps <= 0 {
		fps = 20
	}
	return &Engine{fps: fps}
}

// TickCmd returns a Bubble Tea command that fires a TickMsg after one frame interval.
func (e *Engine) TickCmd() bubbletea.Cmd {
	return bubbletea.Tick(time.Second/time.Duration(e.fps), func(t time.Time) bubbletea.Msg {
		return TickMsg{Tick: e.tick}
	})
}

// Advance increments the internal tick counter and returns a TickCmd for the next frame.
// Call this when you receive a TickMsg to schedule the next one.
func (e *Engine) Advance() bubbletea.Cmd {
	if e.paused {
		return nil
	}
	e.tick++
	return e.TickCmd()
}

// CurrentTick returns the current tick counter value.
func (e *Engine) CurrentTick() int { return e.tick }

// FPS returns the configured frames per second.
func (e *Engine) FPS() int { return e.fps }

// Pause stops the engine from scheduling new ticks.
func (e *Engine) Pause() { e.paused = true }

// Resume restarts the engine.
func (e *Engine) Resume() { e.paused = false }
