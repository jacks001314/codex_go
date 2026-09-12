package tea

import (
	"errors"
	"testing"

	codextui "codex_go/tui"
)

// TestTerminalSizeMonitorGate covers Rust #43603's gate: the monitor applies
// only on Unix under tmux.
func TestTerminalSizeMonitorGate(t *testing.T) {
	tmux := func(key string) string {
		if key == "TMUX" {
			return "/tmp/tmux-1000/default,123,0"
		}
		return ""
	}
	if terminalSizeMonitorNeeded("windows", tmux) {
		t.Fatal("Windows must not run the tmux size monitor")
	}
	if !terminalSizeMonitorNeeded("linux", tmux) {
		t.Fatal("Unix under tmux must run the size monitor")
	}
	if terminalSizeMonitorNeeded("darwin", func(string) string { return "" }) {
		t.Fatal("a non-tmux terminal must not run the size monitor")
	}
	if terminalSizeMonitorNeeded("linux", nil) {
		t.Fatal("a nil environment must not enable the monitor")
	}
}

// TestTerminalSizePollRecoversMissedResize covers the poll behavior: a changed
// geometry re-applies the layout, an unchanged one is a no-op, and a probe
// failure is ignored.
func TestTerminalSizePollRecoversMissedResize(t *testing.T) {
	width, height := 100, 40
	model := NewModel(codextui.NewState(nil), Options{
		Width:        width,
		Height:       height,
		TerminalSize: func() (int, int, error) { return width, height, nil },
	})
	model.sizeMonitorEnabled = true

	// Unchanged geometry: no reflow.
	transcriptWidth := model.transcript.Width
	model.applyTerminalSizePoll()
	if model.width != width || model.transcript.Width != transcriptWidth {
		t.Fatalf("unchanged poll altered the layout: %d/%d", model.width, model.transcript.Width)
	}

	// A missed resize is recovered.
	width, height = 120, 50
	model.applyTerminalSizePoll()
	if model.width != 120 || model.height != 50 || model.transcript.Width != 120 {
		t.Fatalf("missed resize not applied: %dx%d transcript=%d", model.width, model.height, model.transcript.Width)
	}

	// A probe failure is ignored.
	model.terminalSize = func() (int, int, error) { return 0, 0, errors.New("no tty") }
	model.applyTerminalSizePoll()
	if model.width != 120 || model.height != 50 {
		t.Fatalf("failed poll altered the layout: %dx%d", model.width, model.height)
	}
}

// TestTerminalSizeMonitorTickGate pins that a disabled monitor schedules no
// tick.
func TestTerminalSizeMonitorTickGate(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 40})
	model.sizeMonitorEnabled = false
	if cmd := model.sizeMonitorTickCmd(); cmd != nil {
		t.Fatal("a disabled size monitor must not schedule a tick")
	}
	model.sizeMonitorEnabled = true
	if cmd := model.sizeMonitorTickCmd(); cmd == nil {
		t.Fatal("an enabled size monitor must schedule a tick")
	}
}
