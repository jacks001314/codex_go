package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/tui/history_cell"
)

// BackgroundTerminal is one live background terminal of the displayed thread,
// as reported by the app server (Rust #59319 ThreadBackgroundTerminal).
type BackgroundTerminal struct {
	ItemID    string
	ProcessID string
	Command   string
}

// ListBackgroundTerminalsFunc reads every page of a thread's live background
// terminals from the owning app server.
type ListBackgroundTerminalsFunc func(threadID string) ([]BackgroundTerminal, error)

// BackgroundTerminalsLoadedMsg delivers the terminal list fetched after a
// history replay. The request id invalidates a load that was superseded by a
// thread reset or a newer restore.
type BackgroundTerminalsLoadedMsg struct {
	RequestID uint64
	Terminals []BackgroundTerminal
}

// terminalRestoreState tracks one in-flight restore. Exited records the call
// ids of terminals that finished while the request was pending, so they are not
// restored as live (Rust #59319 pending_terminal_restore).
type terminalRestoreState struct {
	id     uint64
	exited map[string]struct{}
}

// beginBackgroundTerminalRestore starts a restore for the displayed thread and
// returns the command that fetches it. It returns nil when no loader is wired.
func (m *Model) beginBackgroundTerminalRestore() bubbletea.Cmd {
	if m == nil || m.onListBackgroundTerminals == nil {
		return nil
	}
	threadID := strings.TrimSpace(m.currentThreadID())
	if threadID == "" {
		return nil
	}
	m.terminalRestoreID++
	requestID := m.terminalRestoreID
	m.pendingTerminalRestore = &terminalRestoreState{id: requestID, exited: map[string]struct{}{}}
	loader := m.onListBackgroundTerminals
	return func() bubbletea.Msg {
		terminals, err := loader(threadID)
		if err != nil {
			terminals = nil
		}
		return BackgroundTerminalsLoadedMsg{RequestID: requestID, Terminals: terminals}
	}
}

// requestBackgroundTerminalRestore marks that the next Update should ask the
// owning app server for the displayed thread's live terminals, mirroring Rust's
// post-replay RestoreBackgroundTerminals event.
func (m *Model) requestBackgroundTerminalRestore() {
	if m == nil || m.onListBackgroundTerminals == nil {
		return
	}
	m.backgroundTerminalRestoreRequested = true
}

// consumeBackgroundTerminalRestore turns a pending request into its command.
func (m *Model) consumeBackgroundTerminalRestore() bubbletea.Cmd {
	if m == nil || !m.backgroundTerminalRestoreRequested {
		return nil
	}
	m.backgroundTerminalRestoreRequested = false
	return m.beginBackgroundTerminalRestore()
}

// restoreBackgroundTerminals merges a fetched list into the tracked processes,
// skipping terminals that exited while the request was pending and processes
// that are already tracked (Rust #59319 restore_background_terminals).
func (m *Model) restoreBackgroundTerminals(message BackgroundTerminalsLoadedMsg) {
	if m == nil {
		return
	}
	pending := m.pendingTerminalRestore
	if pending == nil || pending.id != message.RequestID {
		return
	}
	m.pendingTerminalRestore = nil
	for _, terminal := range message.Terminals {
		itemID := strings.TrimSpace(terminal.ItemID)
		if itemID != "" {
			if _, exited := pending.exited[itemID]; exited {
				continue
			}
		}
		processID := strings.TrimSpace(terminal.ProcessID)
		key := firstNonEmpty(processID, itemID)
		if key != "" && m.hasTrackedUnifiedExecProcess(key) {
			continue
		}
		m.commandLifecycle.TrackUnifiedExecProcessBegin(itemID, processID, terminal.Command)
	}
	m.refreshTranscript()
}

// clearBackgroundTerminals drops tracking and invalidates any pending restore
// (Rust #59319 clear_background_terminals). It runs on thread reset and /stop.
func (m *Model) clearBackgroundTerminals() {
	if m == nil {
		return
	}
	m.pendingTerminalRestore = nil
	m.backgroundTerminalRestoreRequested = false
	m.commandLifecycle.UnifiedExecProcesses = nil
	m.backgroundProcesses = nil
}

// backgroundTerminalDetails renders the tracked processes for the /ps view.
func (m *Model) backgroundTerminalDetails() []historycell.UnifiedExecProcessDetails {
	if m == nil {
		return nil
	}
	details := make([]historycell.UnifiedExecProcessDetails, 0, len(m.backgroundProcesses)+len(m.commandLifecycle.UnifiedExecProcesses))
	details = append(details, cloneUnifiedExecProcessDetails(m.backgroundProcesses)...)
	for _, process := range m.commandLifecycle.UnifiedExecProcesses {
		details = append(details, historycell.UnifiedExecProcessDetails{
			CommandDisplay: process.CommandDisplay,
			RecentChunks:   append([]string(nil), process.RecentChunks...),
		})
	}
	return details
}

// hasTrackedUnifiedExecProcess reports whether key names a tracked process.
func (m *Model) hasTrackedUnifiedExecProcess(key string) bool {
	if m == nil || key == "" {
		return false
	}
	for i := range m.commandLifecycle.UnifiedExecProcesses {
		if m.commandLifecycle.UnifiedExecProcesses[i].Key == key {
			return true
		}
	}
	return false
}
