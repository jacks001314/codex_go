package tea

import (
	keymsg "codex_go/tui/keymsg"
	"os"
	"runtime"
	"strings"
	"time"

	bubbletea "charm.land/bubbletea/v2"
)

// Bubble Tea reports an unrecognized CSI sequence through an unexported
// []byte message whose String form is stable. Windows Terminal maps Shift+Enter
// to ESC[13;2u; convert that exact sequence to the configured insert-newline
// action instead of silently swallowing it (Rust #50720).
const windowsMappedShiftEnterCSI = "?CSI[49 51 59 50 117]?"

const windowsMappedShiftEnterTimeout = 50 * time.Millisecond

type windowsKeySequenceTimeoutMsg struct {
	generation uint64
}

func windowsShiftEnterKeys() []keymsg.KeyMsg {
	return []keymsg.KeyMsg{
		{Type: keymsg.KeyEsc},
		{Type: keymsg.KeyRunes, Runes: []rune{'['}},
		{Type: keymsg.KeyRunes, Runes: []rune{'1'}},
		{Type: keymsg.KeyRunes, Runes: []rune{'3'}},
		{Type: keymsg.KeyRunes, Runes: []rune{';'}},
		{Type: keymsg.KeyRunes, Runes: []rune{'2'}},
		{Type: keymsg.KeyRunes, Runes: []rune{'u'}},
	}
}

func (m *Model) handleWindowsMappedShiftEnter(message bubbletea.Msg) (bool, bubbletea.Cmd) {
	if runtime.GOOS != "windows" {
		return false, nil
	}
	if timeout, ok := message.(windowsKeySequenceTimeoutMsg); ok {
		if timeout.generation != m.windowsKeySequenceGeneration || len(m.windowsKeySequence) == 0 {
			return false, nil
		}
		pending := append([]keymsg.KeyMsg(nil), m.windowsKeySequence...)
		m.windowsKeySequence = nil
		m.windowsKeySequenceGeneration++
		return true, m.replayWindowsKeyMessages(pending)
	}
	if key, ok := message.(keymsg.KeyMsg); ok {
		if !m.windowsTerminalMappedInput {
			return false, nil
		}
		if handled, cmd := m.handleWindowsMappedShiftEnterKey(key); handled {
			return true, cmd
		}
	}
	stringer, ok := message.(interface{ String() string })
	if !ok || stringer.String() != windowsMappedShiftEnterCSI {
		return false, nil
	}
	return true, m.applyWindowsShiftEnter()
}

// WindowsTerminalMappedInputEnabled reports whether the process is running
// under Windows Terminal, whose Shift+Enter mapping requires the sequence
// decoder.
func WindowsTerminalMappedInputEnabled() bool {
	if runtime.GOOS != "windows" {
		return false
	}
	if strings.TrimSpace(os.Getenv("WT_SESSION")) != "" || strings.TrimSpace(os.Getenv("WT_PROFILE_ID")) != "" {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(os.Getenv("TERM_PROGRAM")), "Windows_Terminal")
}

func (m *Model) handleWindowsMappedShiftEnterKey(key keymsg.KeyMsg) (bool, bubbletea.Cmd) {
	if key.Paste || key.Alt {
		return m.flushWindowsKeySequenceWith(key)
	}
	sequence := windowsShiftEnterKeys()
	if len(m.windowsKeySequence) == 0 {
		if key.Type != sequence[0].Type {
			return false, nil
		}
		m.windowsKeySequence = append(m.windowsKeySequence, key)
		m.windowsKeySequenceGeneration++
		generation := m.windowsKeySequenceGeneration
		return true, func() bubbletea.Msg {
			time.Sleep(windowsMappedShiftEnterTimeout)
			return windowsKeySequenceTimeoutMsg{generation: generation}
		}
	}
	next := len(m.windowsKeySequence)
	if next >= len(sequence) || !windowsKeyMessagesEqual(key, sequence[next]) {
		return m.flushWindowsKeySequenceWith(key)
	}
	m.windowsKeySequence = append(m.windowsKeySequence, key)
	if len(m.windowsKeySequence) < len(sequence) {
		return true, nil
	}
	m.windowsKeySequence = nil
	m.windowsKeySequenceGeneration++
	return true, m.applyWindowsShiftEnter()
}

func (m *Model) flushWindowsKeySequenceWith(key keymsg.KeyMsg) (bool, bubbletea.Cmd) {
	pending := append([]keymsg.KeyMsg(nil), m.windowsKeySequence...)
	pending = append(pending, key)
	m.windowsKeySequence = nil
	m.windowsKeySequenceGeneration++
	return true, m.replayWindowsKeyMessages(pending)
}

func (m *Model) replayWindowsKeyMessages(messages []keymsg.KeyMsg) bubbletea.Cmd {
	commands := make([]bubbletea.Cmd, 0, len(messages))
	for _, message := range messages {
		_, cmd := m.updateMessage(message)
		commands = append(commands, cmd)
	}
	return bubbletea.Batch(commands...)
}

func (m *Model) applyWindowsShiftEnter() bubbletea.Cmd {
	// The sequence is consumed either way. Interactive surfaces other than the
	// main composer are left to their own key handling.
	if m == nil || m.modal != nil || m.overlay != nil || m.slashPopup.Active ||
		m.skillPopup.Active || m.mentionPopup != nil || m.vimSearchMode ||
		m.readOnlyThread || m.windowsSandboxSetupActive || m.editorActive ||
		m.misalignmentPolicyStopped {
		return nil
	}
	if !m.keyMatches("editor", "insert_newline", "shift-enter") {
		return nil
	}
	return m.insertComposerNewline()
}

func windowsKeyMessagesEqual(left keymsg.KeyMsg, right keymsg.KeyMsg) bool {
	if left.Type != right.Type || left.Alt != right.Alt || left.Paste != right.Paste {
		return false
	}
	if len(left.Runes) != len(right.Runes) {
		return false
	}
	for index := range left.Runes {
		if left.Runes[index] != right.Runes[index] {
			return false
		}
	}
	return true
}
