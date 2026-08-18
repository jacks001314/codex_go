package tea

import (
	"runtime"
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	agentsoverview "codex_go/tui/agents_overview"
)

// In-session `/agents` dashboard (Rust #39094/#39112). The dashboard is a
// full-screen surface in the tea Model driven by the shared
// tui/agents_overview core. Data comes from host callbacks: the remote TUI
// talks to the shared app server, while the local embedded TUI marks the
// dashboard unavailable (Rust embedded branch) and offers to start the
// background server on Unix.

type AgentsOverviewRefreshFunc func(currentThreadID string) ([]agentsoverview.Row, error)
type AgentsOverviewDispatchFunc func(prompt string, cwd string) (string, error)
type AgentsOverviewStopFunc func(threadID string) error
type AgentsOverviewRenameFunc func(threadID string, name string) error
type AgentsDaemonStartFunc func() error

type agentsOverviewListMsg struct {
	rows      []agentsoverview.Row
	err       error
	requestID int
}

type agentsOverviewDispatchMsg struct {
	threadID string
	err      error
}

type agentsOverviewStopMsg struct {
	err error
}

type agentsOverviewRenameMsg struct {
	err error
}

type agentsOverviewDaemonMsg struct {
	err error
}

// applyAgentsCommand mirrors Rust open_agents_overview: the embedded branch
// shows the "Shared agents unavailable" selection view, otherwise the
// dashboard opens and refreshes loaded root sessions.
func (m *Model) applyAgentsCommand() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if m.agentsOverviewEmbedded {
		return m.openAgentsUnavailableSelection()
	}
	m.agentsOverview = agentsoverview.New(nil, "", false)
	m.agentsOverviewNotice = ""
	m.agentsOverviewBusy = false
	m.agentsOverviewRefresh = 0
	m.agentsOverviewPending = false
	m.agentsOverviewInflight = false
	return m.refreshAgentsOverviewCmd()
}

func (m *Model) openAgentsUnavailableSelection() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	options := []ModalOption{
		{ID: "return", Label: "Return to this session"},
	}
	if runtime.GOOS != "windows" && m.onStartAgentsDaemon != nil {
		options = append([]ModalOption{{
			ID:          "start-daemon",
			Label:       "Start background server",
			Description: "Open `codex agents` in another terminal afterward.",
		}}, options...)
	}
	m.openModal(ModalRequestMsg{
		ID:         "agents-unavailable",
		Kind:       ModalKindAgents,
		Title:      "Shared agents unavailable",
		Body:       "This session isn't connected to a shared background server.",
		Options:    options,
		FooterHint: "Starting a background server will not interrupt or move this session.",
	})
	return nil
}

func (m *Model) applyAgentsModalOption(optionID string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	switch optionID {
	case "start-daemon":
		if m.onStartAgentsDaemon == nil {
			m.notice = "Starting a background server is unavailable in this runtime."
			return nil
		}
		return func() bubbletea.Msg {
			err := m.onStartAgentsDaemon()
			return agentsOverviewDaemonMsg{err: err}
		}
	default: // "return"
		m.notice = ""
		return nil
	}
}

func (m *Model) refreshAgentsOverviewCmd() bubbletea.Cmd {
	if m == nil || m.agentsOverview == nil {
		return nil
	}
	if m.onAgentsOverviewRefresh == nil {
		m.agentsOverviewNotice = "The agents dashboard is unavailable in this runtime."
		return nil
	}
	if m.agentsOverviewInflight {
		// Coalesce notification-driven refreshes (Rust refresh_pending).
		m.agentsOverviewPending = true
		return nil
	}
	m.agentsOverviewRefresh++
	requestID := m.agentsOverviewRefresh
	m.agentsOverviewInflight = true
	currentThreadID := ""
	if m.State != nil {
		currentThreadID = strings.TrimSpace(m.State.ThreadID)
	}
	return func() bubbletea.Msg {
		rows, err := m.onAgentsOverviewRefresh(currentThreadID)
		return agentsOverviewListMsg{rows: rows, err: err, requestID: requestID}
	}
}

func (m *Model) applyAgentsOverviewList(message agentsOverviewListMsg) bubbletea.Cmd {
	if m == nil || m.agentsOverview == nil {
		return nil
	}
	if message.requestID != 0 && message.requestID != m.agentsOverviewRefresh {
		return nil
	}
	m.agentsOverviewInflight = false
	if message.err != nil {
		m.agentsOverviewNotice = "Failed to load shared agents: " + strings.TrimSpace(message.err.Error())
		if m.agentsOverviewPending {
			m.agentsOverviewPending = false
			return m.refreshAgentsOverviewCmd()
		}
		return nil
	}
	m.agentsOverviewNotice = ""
	m.agentsOverview.ApplyRefresh(message.rows, m.agentsOverview.SelectedThreadID())
	if m.agentsOverviewPending {
		m.agentsOverviewPending = false
		return m.refreshAgentsOverviewCmd()
	}
	return nil
}

// updateAgentsOverviewKey routes keys to the dashboard while it is active.
func (m *Model) updateAgentsOverviewKey(msg bubbletea.KeyMsg) bubbletea.Cmd {
	if m == nil || m.agentsOverview == nil {
		return nil
	}
	switch msg.String() {
	case "up", "k":
		m.agentsOverview.MoveSelection(false)
	case "down", "j":
		m.agentsOverview.MoveSelection(true)
	case "pgup":
		m.agentsOverview.PageUp()
	case "pgdown":
		m.agentsOverview.PageDown()
	case "home":
		m.agentsOverview.JumpTop()
	case "end":
		m.agentsOverview.JumpBottom()
	case "enter":
		prompt := strings.TrimSpace(m.agentsOverview.State.Input)
		switch action := m.agentsOverview.Activate(); action {
		case agentsoverview.ActionDispatchTask:
			return m.dispatchAgentsOverviewCmd(prompt)
		case agentsoverview.ActionRenameThread:
			return m.renameAgentsOverviewCmd(prompt)
		case agentsoverview.ActionOpenThread:
			return m.openAgentsOverviewThread(m.agentsOverview.SelectedThreadID())
		}
	case "esc":
		m.agentsOverview.Cancel()
		if m.agentsOverview.Completion != agentsoverview.CompletionNone {
			m.closeAgentsOverview()
			m.notice = ""
		}
	case "backspace":
		m.agentsOverview.Backspace()
	case "ctrl+f":
		m.agentsOverview.ToggleSearch()
	case "ctrl+s":
		m.agentsOverview.ToggleGrouping()
	case "ctrl+n":
		m.agentsOverview.ClearNew()
	case "ctrl+r":
		m.agentsOverview.BeginRename()
	case "ctrl+x":
		if action := m.agentsOverview.StopSelected(); action == agentsoverview.ActionStopThread {
			return m.stopAgentsOverviewCmd(m.agentsOverview.SelectedThreadID())
		}
	case "ctrl+c":
		return bubbletea.Quit
	default:
		if msg.Type == bubbletea.KeyRunes {
			for _, r := range msg.Runes {
				m.agentsOverview.TypeChar(r)
			}
		}
	}
	return nil
}

func (m *Model) dispatchAgentsOverviewCmd(prompt string) bubbletea.Cmd {
	if m == nil || m.agentsOverview == nil || m.onAgentsOverviewDispatch == nil || prompt == "" || m.agentsOverviewBusy {
		return nil
	}
	cwd := ""
	if !m.agentsOverview.State.StatusGrouping {
		if row := m.agentsOverview.SelectedRow(); row != nil {
			cwd = strings.TrimSpace(row.CWD)
		}
	}
	m.agentsOverviewBusy = true
	return func() bubbletea.Msg {
		threadID, err := m.onAgentsOverviewDispatch(prompt, cwd)
		return agentsOverviewDispatchMsg{threadID: threadID, err: err}
	}
}

func (m *Model) stopAgentsOverviewCmd(threadID string) bubbletea.Cmd {
	if m == nil || m.agentsOverview == nil || m.onAgentsOverviewStop == nil || strings.TrimSpace(threadID) == "" || m.agentsOverviewBusy {
		return nil
	}
	m.agentsOverviewBusy = true
	return func() bubbletea.Msg {
		err := m.onAgentsOverviewStop(threadID)
		return agentsOverviewStopMsg{err: err}
	}
}

func (m *Model) renameAgentsOverviewCmd(name string) bubbletea.Cmd {
	if m == nil || m.agentsOverview == nil || m.onAgentsOverviewRename == nil || strings.TrimSpace(name) == "" {
		return nil
	}
	threadID := m.agentsOverview.SelectedThreadID()
	if threadID == "" {
		return nil
	}
	m.agentsOverviewBusy = true
	return func() bubbletea.Msg {
		err := m.onAgentsOverviewRename(threadID, name)
		return agentsOverviewRenameMsg{err: err}
	}
}

// openAgentsOverviewThread closes the dashboard and attaches to the selected
// root session through the same switch-agent path used by the subagent
// picker (Rust select_agents_overview_thread).
func (m *Model) openAgentsOverviewThread(threadID string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	current := ""
	if m.State != nil {
		current = strings.TrimSpace(m.State.ThreadID)
	}
	m.closeAgentsOverview()
	if threadID == "" {
		m.notice = "This session is no longer available."
		return nil
	}
	if threadID == current {
		m.notice = "Already showing this session."
		return nil
	}
	return m.applyAgentModalOption(threadID)
}

func (m *Model) closeAgentsOverview() {
	if m == nil {
		return
	}
	m.agentsOverview = nil
	m.agentsOverviewNotice = ""
	m.agentsOverviewBusy = false
	m.agentsOverviewRefresh = 0
	m.agentsOverviewPending = false
	m.agentsOverviewInflight = false
	m.refreshTranscript()
}

// renderAgentsOverview renders the dashboard full-screen (Rust: full-screen
// dashboard opened by /agents).
func (m *Model) renderAgentsOverview() string {
	if m == nil || m.agentsOverview == nil {
		return ""
	}
	lines := m.agentsOverview.Render(m.width, m.height)
	if m.agentsOverviewNotice != "" {
		lines = append(lines, "  "+m.agentsOverviewNotice)
	}
	return strings.Join(lines, "\n")
}
