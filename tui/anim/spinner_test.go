package anim

import (
	"testing"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"
)

func TestNewEngineDefaults(t *testing.T) {
	e := NewEngine(0)
	if e.FPS() != 20 {
		t.Errorf("default FPS = %d, want 20", e.FPS())
	}
}

func TestNewEngineCustomFPS(t *testing.T) {
	e := NewEngine(30)
	if e.FPS() != 30 {
		t.Errorf("FPS = %d, want 30", e.FPS())
	}
}

func TestEngineTick(t *testing.T) {
	e := NewEngine(20)
	cmd := e.TickCmd()
	if cmd == nil {
		t.Fatal("TickCmd returned nil")
	}
	// Verify the message type by calling the command
	msg := cmd()
	tickMsg, ok := msg.(TickMsg)
	if !ok {
		t.Fatalf("expected TickMsg, got %T", msg)
	}
	if tickMsg.Tick != 0 {
		t.Errorf("first tick = %d, want 0", tickMsg.Tick)
	}
}

func TestEngineAdvance(t *testing.T) {
	e := NewEngine(20)
	if e.CurrentTick() != 0 {
		t.Errorf("initial tick = %d, want 0", e.CurrentTick())
	}

	cmd := e.Advance()
	if cmd == nil {
		t.Fatal("Advance returned nil")
	}
	if e.CurrentTick() != 1 {
		t.Errorf("after advance tick = %d, want 1", e.CurrentTick())
	}
}

func TestEnginePauseResume(t *testing.T) {
	e := NewEngine(20)
	e.Pause()
	cmd := e.Advance()
	if cmd != nil {
		t.Error("Advance should return nil when paused")
	}
	if e.CurrentTick() != 0 {
		t.Error("tick should not advance when paused")
	}

	e.Resume()
	cmd = e.Advance()
	if cmd == nil {
		t.Error("Advance should return non-nil after resume")
	}
}

func TestSpinnerDots(t *testing.T) {
	e := NewEngine(20)
	s := NewSpinner(SpinnerConfig{
		Label: "Thinking",
		Mode:  SpinnerDots,
	}, e)

	// Verify 4-frame dot cycle
	if len(s.frames) != 4 {
		t.Fatalf("dots frames = %d, want 4", len(s.frames))
	}

	expectedFrames := []string{
		"Thinking.  ",
		"Thinking.. ",
		"Thinking...",
		"Thinking   ",
	}
	for i, expected := range expectedFrames {
		if s.frames[i] != expected {
			t.Errorf("frame[%d] = %q, want %q", i, s.frames[i], expected)
		}
	}
}

func TestSpinnerViewCycles(t *testing.T) {
	e := NewEngine(20)
	s := NewSpinner(SpinnerConfig{
		Label: "Loading",
		Mode:  SpinnerDots,
	}, e)

	// Simulate ticks
	for tick := 0; tick < 10; tick++ {
		msg := bubbletea.Tick(time.Millisecond, func(t time.Time) bubbletea.Msg {
			return TickMsg{Tick: tick}
		})
		view := s.View()
		cmd := s.Update(msg())
		if cmd == nil && tick > 0 {
			t.Errorf("Update returned nil on tick %d", tick)
		}
		if view == "" {
			t.Errorf("View returned empty on tick %d", tick)
		}
	}
}

func TestSpinnerScramble(t *testing.T) {
	e := NewEngine(20)
	s := NewSpinner(SpinnerConfig{
		Label:       "Processing",
		Mode:        SpinnerScramble,
		ScrambleSet: "0123456789abcdef",
	}, e)

	if len(s.frames) == 0 {
		t.Fatal("scramble frames is empty")
	}

	// All frames should start with "Processing"
	for i, frame := range s.frames {
		if len(frame) < len("Processing") {
			t.Errorf("frame[%d] too short: %q", i, frame)
		}
	}
}

func TestSpinnerPulse(t *testing.T) {
	e := NewEngine(20)
	s := NewSpinner(SpinnerConfig{
		Label: "Working",
		Mode:  SpinnerPulse,
	}, e)

	if len(s.frames) != 20 {
		t.Errorf("pulse frames = %d, want 20", len(s.frames))
	}
}

func TestSpinnerFrameCache(t *testing.T) {
	e := NewEngine(20)
	cfg := SpinnerConfig{Label: "Test", Mode: SpinnerDots}

	s1 := NewSpinner(cfg, e)
	s2 := NewSpinner(cfg, e)

	// Same config should share frames (pointer equality)
	if &s1.frames[0] != &s2.frames[0] {
		t.Error("Spinners with identical config should share frame cache")
	}
}

func TestSpinnerNilEngine(t *testing.T) {
	s := NewSpinner(SpinnerConfig{Label: "A", Mode: SpinnerDots}, nil)
	if s.engine == nil {
		t.Fatal("nil engine should be replaced with default")
	}
	if s.engine.FPS() != 20 {
		t.Errorf("default engine FPS = %d, want 20", s.engine.FPS())
	}
}

func TestSpinnerInitCmd(t *testing.T) {
	e := NewEngine(20)
	s := NewSpinner(SpinnerConfig{Label: "X", Mode: SpinnerDots}, e)

	cmd := s.InitCmd()
	if cmd == nil {
		t.Fatal("InitCmd returned nil")
	}
}

func TestSpinnerViewWithNoFrames(t *testing.T) {
	// Edge case: manually create spinner with empty frames
	s := &Spinner{
		config: SpinnerConfig{Label: "Fallback"},
		engine: NewEngine(20),
		frames: nil,
	}
	if s.View() != "Fallback" {
		t.Errorf("View with no frames = %q, want 'Fallback'", s.View())
	}
}

func TestConfigHashStability(t *testing.T) {
	cfg := SpinnerConfig{Label: "Test", Mode: SpinnerDots}
	h1 := configHash(cfg)
	h2 := configHash(cfg)
	if h1 != h2 {
		t.Errorf("configHash not stable: %q vs %q", h1, h2)
	}
}

func TestConfigHashUniqueness(t *testing.T) {
	h1 := configHash(SpinnerConfig{Label: "A", Mode: SpinnerDots})
	h2 := configHash(SpinnerConfig{Label: "B", Mode: SpinnerDots})
	h3 := configHash(SpinnerConfig{Label: "A", Mode: SpinnerScramble})

	if h1 == h2 {
		t.Error("different labels should produce different hashes")
	}
	if h1 == h3 {
		t.Error("different modes should produce different hashes")
	}
}
