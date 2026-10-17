package tea

import bubbletea "github.com/charmbracelet/bubbletea"

const (
	clearVisibleTerminalSequence    = "\x1b[2J\x1b[H"
	clearScrollbackTerminalSequence = "\x1b[2J\x1b[3J\x1b[H"
)

// clearTerminalForCommand mirrors Rust's /clear path: inline sessions clear
// scrollback and the visible screen, while a temporary alternate-screen overlay
// clears only its visible screen before the thread reset closes it.
func (m *Model) clearTerminalForCommand() bubbletea.Cmd {
	if m == nil || m.terminalClearWriter == nil {
		return nil
	}
	sequence := clearScrollbackTerminalSequence
	if m.overlayAltScreen || m.sessionPickerAltScreen {
		sequence = clearVisibleTerminalSequence
	}
	return m.terminalClearWriter(sequence)
}
