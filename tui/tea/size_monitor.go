package tea

import (
	"os"
	"strings"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"
)

// Rust parity: codex-rs/tui/src/tui/size_monitor.rs (#43603). tmux can drop
// SIGWINCH, so under tmux (Unix only) the TUI polls the terminal size and
// synthesizes the missed resize instead of querying geometry on every frame.
const terminalSizePollInterval = 500 * time.Millisecond

// terminalSizePollMsg triggers one terminal-size probe.
type terminalSizePollMsg struct{}

// terminalSizeMonitorNeeded mirrors SizeMonitor::start's gate: Unix under tmux.
func terminalSizeMonitorNeeded(goos string, env func(string) string) bool {
	if goos == "windows" {
		return false
	}
	if env == nil {
		return false
	}
	return strings.TrimSpace(env("TMUX")) != ""
}

// defaultTerminalSize queries the process terminal's current geometry.
func defaultTerminalSize() (int, int, error) {
	return term.GetSize(os.Stdout.Fd())
}

// sizeMonitorTickCmd schedules the next terminal-size probe.
func (m *Model) sizeMonitorTickCmd() bubbletea.Cmd {
	if m == nil || !m.sizeMonitorEnabled {
		return nil
	}
	return bubbletea.Tick(terminalSizePollInterval, func(time.Time) bubbletea.Msg {
		return terminalSizePollMsg{}
	})
}

// applyTerminalSizePoll re-applies the terminal size when it changed without a
// resize notification (Rust SizeMonitor publish).
func (m *Model) applyTerminalSizePoll() {
	if m == nil {
		return
	}
	getSize := m.terminalSize
	if getSize == nil {
		getSize = defaultTerminalSize
	}
	width, height, err := getSize()
	if err != nil || width <= 0 || height <= 0 {
		return
	}
	if width == m.width && height == m.height {
		return
	}
	m.resize(width, height)
}
