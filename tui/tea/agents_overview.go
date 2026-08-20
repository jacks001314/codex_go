package tea

import (
	"runtime"
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
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
	m.agentsOverviewPendingDraft = nil
	m.agentsOverview = agentsoverview.New(nil, "", false)
	m.agentsOverviewNotice = ""
	m.agentsOverviewBusy = false
	m.agentsOverviewRefresh = 0
	m.agentsOverviewPending = false
	m.agentsOverviewInflight = false
	m.applyAgentsOverviewKeymapHints()
	return m.refreshAgentsOverviewCmd()
}

// applyAgentsOverviewKeymapHints resolves the agents-dashboard shortcuts from
// the user's keymap so the footer renders custom bindings (Rust #39142
// AgentsKeymap::primary_hint).
func (m *Model) applyAgentsOverviewKeymapHints() {
	if m == nil || m.agentsOverview == nil {
		return
	}
	for _, hint := range []struct {
		action string
		key    string
	}{
		{action: agentsoverview.ShortcutHintSearch, key: "agents.search"},
		{action: agentsoverview.ShortcutHintToggleGrouping, key: "agents.toggle_grouping"},
		{action: agentsoverview.ShortcutHintRename, key: "agents.rename"},
		{action: agentsoverview.ShortcutHintStop, key: "agents.stop"},
	} {
		context, action, _ := strings.Cut(hint.key, ".")
		bindings, _, _ := codextui.ResolvedKeymapBindings(m.keymapConfig, context, action)
		binding := ""
		if len(bindings) > 0 {
			binding = bindings[0]
		}
		m.agentsOverview.SetShortcutHint(hint.action, binding)
	}
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
	keySpec := keySpecFromKeyMsg(msg)
	handled := false
	switch msg.String() {
	case "up", "k":
		m.agentsOverview.MoveSelection(false)
		handled = true
	case "down", "j":
		m.agentsOverview.MoveSelection(true)
		handled = true
	case "pgup":
		m.agentsOverview.PageUp()
		handled = true
	case "pgdown":
		m.agentsOverview.PageDown()
		handled = true
	case "home":
		m.agentsOverview.JumpTop()
		handled = true
	case "end":
		m.agentsOverview.JumpBottom()
		handled = true
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
		handled = true
	case "esc":
		m.agentsOverview.Cancel()
		if m.agentsOverview.Completion != agentsoverview.CompletionNone {
			m.closeAgentsOverview()
			m.notice = ""
		}
		handled = true
	case "backspace":
		m.agentsOverview.Backspace()
		handled = true
	}
	if m.keyMatches("agents", "search", keySpec) {
		m.agentsOverview.ToggleSearch()
		handled = true
	}
	if m.keyMatches("agents", "toggle_grouping", keySpec) {
		m.agentsOverview.ToggleGrouping()
		handled = true
	}
	if m.keyMatches("agents", "new_task", keySpec) {
		m.agentsOverview.ClearNew()
		handled = true
	}
	if m.keyMatches("agents", "rename", keySpec) {
		m.agentsOverview.BeginRename()
		handled = true
	}
	if m.keyMatches("agents", "stop", keySpec) {
		if action := m.agentsOverview.StopSelected(); action == agentsoverview.ActionStopThread {
			return m.stopAgentsOverviewCmd(m.agentsOverview.SelectedThreadID())
		}
		handled = true
	}
	if keySpec == "ctrl-c" {
		return bubbletea.Quit
	}
	if !handled && msg.Type == bubbletea.KeyRunes {
		for _, r := range msg.Runes {
			m.agentsOverview.TypeChar(r)
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
// picker (Rust select_agents_overview_thread). The current thread's composer
// draft is preserved per-thread and the target thread's saved draft is
// restored after the switch completes (Rust input_states / restore).
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
	// Rust select_agents_overview_thread: preserve the current thread's
	// composer draft so returning to it restores what was being composed. The
	// composer is empty here (the /agents slash command consumed it), but the
	// capture keeps the per-thread draft map consistent across chained
	// switches and mirrors the Rust input_states lifecycle.
	if current != "" && current != threadID {
		m.captureAgentsOverviewDraft(current)
	}
	if saved, ok := m.agentsOverviewDrafts[threadID]; ok {
		draft := saved
		m.agentsOverviewPendingDraft = &draft
		delete(m.agentsOverviewDrafts, threadID)
	} else {
		// Rust builds a fresh chat widget when attaching to a thread without
		// a preserved input state, so the composer starts empty instead of
		// carrying the previous thread's draft.
		empty := ""
		m.agentsOverviewPendingDraft = &empty
	}
	return m.applyAgentModalOption(threadID)
}

// captureAgentsOverviewDraft stores the composer draft for a thread so it can
// be restored when the dashboard re-attaches to it (Rust input_states). The
// textarea has no read API for the cursor column, so the draft is the
// composer value (the cursor lands at the end on restore).
func (m *Model) captureAgentsOverviewDraft(threadID string) {
	if m == nil {
		return
	}
	if threadID == "" {
		return
	}
	if m.agentsOverviewDrafts == nil {
		m.agentsOverviewDrafts = map[string]string{}
	}
	draft := m.composer.Value()
	if strings.TrimSpace(draft) == "" {
		delete(m.agentsOverviewDrafts, threadID)
		return
	}
	m.agentsOverviewDrafts[threadID] = draft
}

// restorePendingAgentsOverviewDraft applies the saved draft for the thread
// the dashboard just attached to (Rust restore_thread_input_state). It is
// invoked from the switch-result path after a successful attach.
func (m *Model) restorePendingAgentsOverviewDraft() {
	if m == nil || m.agentsOverviewPendingDraft == nil {
		return
	}
	draft := *m.agentsOverviewPendingDraft
	m.agentsOverviewPendingDraft = nil
	m.composer.SetValue(draft)
}

// discardPendingAgentsOverviewDraft drops a pending draft after a failed
// switch so it is not restored onto the wrong thread later.
func (m *Model) discardPendingAgentsOverviewDraft() {
	if m != nil {
		m.agentsOverviewPendingDraft = nil
	}
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
	lines := m.agentsOverview.RenderStyled(m.width, m.height)
	if m.agentsOverviewNotice != "" {
		lines = append(lines, "  "+m.agentsOverviewNotice)
	}
	return strings.Join(lines, "\n")
}
