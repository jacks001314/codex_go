package tea

import (
	"runtime"
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/features"
	codextui "codex_go/tui"
	agentsoverview "codex_go/tui/agents_overview"
	"codex_go/tui/markdown"
)

// In-session `/agents` dashboard (Rust #39094/#39112). The dashboard is a
// full-screen surface in the tea Model driven by the shared
// tui/agents_overview core. Data comes from host callbacks: the remote TUI
// talks to the shared app server, while the local embedded TUI marks the
// dashboard unavailable (Rust embedded branch) and offers to start the
// background server on Unix.

type AgentsOverviewRefreshFunc func(currentThreadID string) ([]agentsoverview.Row, error)

// AgentsOverviewNewWorktreeFunc creates a managed worktree from the selected
// project's default branch and starts a blank session in it (Rust #45276
// new_agents_overview_worktree). The returned snapshot is the dashboard's
// attach response for the new session.
type AgentsOverviewNewWorktreeFunc func(cwd string) (AgentThreadSwitchResponse, error)

// AgentsOverviewNewSessionFunc starts a blank session in the selected checkout
// without sending an initial turn and returns the attached thread snapshot the
// dashboard switches to (Rust #45255 new_agents_overview_session). A started
// session has no rollout yet, so the snapshot is retained until its first turn.
type AgentsOverviewNewSessionFunc func(cwd string) (AgentThreadSwitchResponse, error)
type AgentsOverviewStopFunc func(threadID string) error
type AgentsOverviewRenameFunc func(threadID string, name string) error
type AgentsOverviewArchiveFunc func(threadID string) error
type AgentsOverviewDeleteFunc func(threadID string) error
type AgentsDaemonStartFunc func() error

// Agents overview lifecycle confirmation (Rust #44433): archive or permanently
// delete the selected task and its child agents after explicit confirmation.
const agentsOverviewLifecycleModalID = "agents-overview-lifecycle"

type agentsOverviewLifecycleAction string

const (
	agentsOverviewActionArchive agentsOverviewLifecycleAction = "archive"
	agentsOverviewActionDelete  agentsOverviewLifecycleAction = "delete"
)

type agentsOverviewLifecycleRequest struct {
	threadID string
	action   agentsOverviewLifecycleAction
}

type agentsOverviewLifecycleMsg struct {
	threadID string
	action   agentsOverviewLifecycleAction
	err      error
}

type agentsOverviewListMsg struct {
	rows      []agentsoverview.Row
	err       error
	requestID int
}

type agentsOverviewNewSessionMsg struct {
	response AgentThreadSwitchResponse
	err      error
}

type agentsOverviewNewWorktreeMsg struct {
	response AgentThreadSwitchResponse
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
	m.agentsOverview.SetWorktreesEnabled(m.agentsOverviewWorktreesEnabled())
	m.wireAgentsOverviewThemeColors(m.agentsOverview)
	// Rust #44424: hidden tasks stay hidden across dashboard close/reopen.
	m.agentsOverview.SetHiddenThreads(m.agentsOverviewHidden)
	m.agentsOverviewNotice = ""
	m.agentsOverviewBusy = false
	m.agentsOverviewRefresh = 0
	m.agentsOverviewPending = false
	m.agentsOverviewInflight = false
	m.agentsOverviewLifecycle = nil
	m.agentsOverviewLifecycleProgress = ""
	m.applyAgentsOverviewKeymapHints()
	return m.refreshAgentsOverviewCmd()
}

// agentsOverviewWorktreesEnabled groups linked checkouts in the dashboard when
// the worktrees feature is on and the session is local (Rust #43279).
func (m *Model) agentsOverviewWorktreesEnabled() bool {
	if m == nil {
		return false
	}
	return m.localSession && features.Enabled(m.featureSettings, "worktrees")
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
		{action: agentsoverview.ShortcutHintHide, key: "agents.hide"},
		{action: agentsoverview.ShortcutHintNewWorktree, key: "agents.new_worktree"},
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

// wireAgentsOverviewThemeColors lets the dashboard color task titles with
// deterministic per-thread identity colors from the active syntax theme
// (Rust #44857). The resolver reads the live theme id so a theme change is
// reflected on the next render.
func (m *Model) wireAgentsOverviewThemeColors(view *agentsoverview.View) {
	if view == nil {
		return
	}
	view.UseThemeColors = m.statusLineUseColors
	view.ThreadColor = func(threadID string) string {
		return codextui.ThreadColorForTheme(threadID, m.tuiTheme)
	}
	// Rust #44752: the task-details prompt preview renders markdown into styled
	// lines. The core cannot measure ANSI, so the renderer returns lines already
	// wrapped to the requested width.
	view.RenderMarkdown = func(text string, width int) []string {
		if width <= 0 || strings.TrimSpace(text) == "" {
			return nil
		}
		rendered, err := markdown.RenderWithThemeCwd(text, width, m.tuiTheme, m.overviewCWD())
		if err != nil {
			return nil
		}
		lines := strings.Split(strings.ReplaceAll(rendered, "\r\n", "\n"), "\n")
		for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
			lines = lines[:len(lines)-1]
		}
		return lines
	}
}

// overviewCWD returns the working directory used to resolve local file links in
// the dashboard's markdown preview.
func (m *Model) overviewCWD() string {
	if m == nil || m.State == nil {
		return ""
	}
	return strings.TrimSpace(m.State.CWD)
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
	m.syncAgentsOverviewUsageLines()
	usageCmd := m.refreshAgentsOverviewUsageCmd()
	if m.agentsOverviewPending {
		m.agentsOverviewPending = false
		return bubbletea.Batch(m.refreshAgentsOverviewCmd(), usageCmd)
	}
	return usageCmd
}

// updateAgentsOverviewKey routes keys to the dashboard while it is active.
func (m *Model) updateAgentsOverviewKey(msg bubbletea.KeyMsg) bubbletea.Cmd {
	if m == nil || m.agentsOverview == nil {
		return nil
	}
	// A modal opened from the dashboard (Rust #44433 lifecycle confirmations)
	// owns input until it is resolved.
	if m.modal != nil {
		return m.updateModal(msg)
	}
	// Rust #44433: an in-flight archive/delete keeps rendering but blocks
	// navigation and task switching until it finishes.
	if m.agentsOverviewLifecycleProgress != "" {
		return nil
	}
	// Rust #45276: creating a worktree pauses every action but cancel/quit.
	if m.agentsOverview.State.CreatingWorktree && msg.String() != "esc" && msg.String() != "ctrl+c" {
		return nil
	}
	selectedBefore := m.agentsOverview.Selected
	keySpec := keySpecFromKeyMsg(msg)
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
	case "right":
		// Rust #44344/#45255: Right opens the selected task unless metadata
		// editing owns the editor; a connection notice keeps the list.
		if m.agentsOverviewNotice == "" && m.agentsOverview.CanOpenWithRight() {
			return m.openAgentsOverviewThread(m.agentsOverview.SelectedThreadID())
		}
	case "enter":
		name := strings.TrimSpace(m.agentsOverview.State.Input)
		switch action := m.agentsOverview.Activate(); action {
		case agentsoverview.ActionRenameThread:
			return m.renameAgentsOverviewCmd(name)
		case agentsoverview.ActionOpenThread:
			return m.openAgentsOverviewThread(m.agentsOverview.SelectedThreadID())
		}
	case "esc":
		m.agentsOverview.Cancel()
		if m.agentsOverview.Completion != agentsoverview.CompletionNone {
			m.closeAgentsOverview()
			m.notice = ""
			return nil
		}
	case "backspace":
		m.agentsOverview.Backspace()
	}
	// Rust #45255: while the search or rename field owns the editor, plain
	// characters edit it instead of triggering a dashboard shortcut.
	if m.agentsOverview.State.Searching || m.agentsOverview.State.Renaming {
		if msg.Type == bubbletea.KeyRunes && !msg.Alt {
			for _, r := range msg.Runes {
				m.agentsOverview.TypeChar(r)
			}
		}
		return nil
	}
	if m.keyMatches("agents", "search", keySpec) {
		m.agentsOverview.ToggleSearch()
	}
	if m.keyMatches("agents", "toggle_grouping", keySpec) {
		m.agentsOverview.ToggleGrouping()
	}
	if m.keyMatches("agents", "new_task", keySpec) {
		return m.newAgentsOverviewSessionCmd()
	}
	if m.keyMatches("agents", "new_worktree", keySpec) {
		if m.agentsOverview == nil || !m.agentsOverviewWorktreesEnabled() {
			return nil
		}
		return m.newAgentsOverviewWorktreeCmd()
	}
	if m.keyMatches("agents", "rename", keySpec) {
		m.agentsOverview.BeginRename()
	}
	if m.keyMatches("agents", "stop", keySpec) {
		if action := m.agentsOverview.StopSelected(); action == agentsoverview.ActionStopThread {
			return m.stopAgentsOverviewCmd(m.agentsOverview.SelectedThreadID())
		}
	}
	if m.keyMatches("agents", "archive", keySpec) {
		if action := m.agentsOverview.ArchiveSelected(); action == agentsoverview.ActionArchiveThread {
			m.openAgentsOverviewLifecycleConfirmation(agentsOverviewActionArchive)
		}
	}
	if m.keyMatches("agents", "delete", keySpec) {
		if action := m.agentsOverview.DeleteSelected(); action == agentsoverview.ActionDeleteThread {
			m.openAgentsOverviewLifecycleConfirmation(agentsOverviewActionDelete)
		}
	}
	if m.keyMatches("agents", "hide", keySpec) {
		// Rust #44424: hide the selected task locally without stopping it.
		if action := m.agentsOverview.HideSelected(); action == agentsoverview.ActionHideThread {
			m.notice = ""
		}
	}
	if keySpec == "ctrl-c" {
		return bubbletea.Quit
	}
	// Rust #44970: the selected task's usage estimate follows the selection.
	if m.agentsOverview != nil && m.agentsOverview.Selected != selectedBefore {
		return m.refreshAgentsOverviewUsageCmd()
	}
	return nil
}

// newAgentsOverviewWorktreeCmd creates a worktree from the selected project's
// default branch and switches to the blank session started inside it (Rust
// #45276). The dashboard shows progress and pauses its actions meanwhile.
func (m *Model) newAgentsOverviewWorktreeCmd() bubbletea.Cmd {
	if m == nil || m.agentsOverview == nil || m.onAgentsOverviewNewWorktree == nil || m.agentsOverviewBusy {
		return nil
	}
	if m.agentsOverviewNotice != "" {
		return nil
	}
	cwd := strings.TrimSpace(m.agentsOverview.State.Input)
	if cwd == "" {
		if row := m.agentsOverview.SelectedRow(); row != nil {
			cwd = strings.TrimSpace(row.CWD)
		}
	}
	m.agentsOverviewBusy = true
	m.agentsOverview.SetCreatingWorktree(true)
	return func() bubbletea.Msg {
		response, err := m.onAgentsOverviewNewWorktree(cwd)
		return agentsOverviewNewWorktreeMsg{response: response, err: err}
	}
}

// applyAgentsOverviewNewWorktree attaches to the session started in a freshly
// created worktree, or reports why the worktree session could not start.
func (m *Model) applyAgentsOverviewNewWorktree(message agentsOverviewNewWorktreeMsg) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	m.agentsOverviewBusy = false
	if m.agentsOverview != nil {
		m.agentsOverview.SetCreatingWorktree(false)
	}
	if message.err != nil || strings.TrimSpace(message.response.Entry.ThreadID) == "" {
		text := strings.TrimSpace(message.err.Error())
		if text == "" {
			text = "the server returned no thread id"
		}
		m.agentsOverviewNotice = "Failed to create worktree session: " + text
		m.refreshTranscript()
		return nil
	}
	return m.applyAgentsOverviewNewSession(agentsOverviewNewSessionMsg{response: message.response})
}

// newAgentsOverviewSessionCmd starts a blank session in the selected checkout
// without sending a turn, so running agents keep running (Rust #45255, the `n`
// shortcut).
func (m *Model) newAgentsOverviewSessionCmd() bubbletea.Cmd {
	if m == nil || m.agentsOverview == nil || m.onAgentsOverviewNewSession == nil || m.agentsOverviewBusy {
		return nil
	}
	// Rust #45255 returns early while offline or when the list is showing a
	// connection notice: a new session needs the app server.
	if m.agentsOverviewNotice != "" {
		return nil
	}
	cwd := ""
	if m.agentsOverview.State.Grouping == agentsoverview.GroupingProject {
		if row := m.agentsOverview.SelectedRow(); row != nil {
			cwd = strings.TrimSpace(row.CWD)
		}
	}
	m.agentsOverviewBusy = true
	return func() bubbletea.Msg {
		response, err := m.onAgentsOverviewNewSession(cwd)
		return agentsOverviewNewSessionMsg{response: response, err: err}
	}
}

// applyAgentsOverviewNewSession attaches to the session the dashboard just
// started. The started thread has no rollout, so its snapshot is retained and
// reused when the dashboard re-opens it before the first turn (Rust
// agents_overview.blank_sessions).
func (m *Model) applyAgentsOverviewNewSession(message agentsOverviewNewSessionMsg) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	m.agentsOverviewBusy = false
	if message.err != nil {
		text := strings.TrimSpace(message.err.Error())
		if text == "" {
			text = "unknown error"
		}
		m.agentsOverviewNotice = "Failed to start session: " + text
		return nil
	}
	threadID := strings.TrimSpace(message.response.Entry.ThreadID)
	if threadID == "" {
		m.agentsOverviewNotice = "Failed to start session: the server returned no thread id"
		return nil
	}
	m.setAgentsOverviewBlankSession(threadID, message.response)
	// Rust attaches a new session with a fresh chat widget, so the composer
	// starts empty.
	empty := ""
	m.agentsOverviewPendingDraft = &empty
	m.closeAgentsOverview()
	m.applyAgentSwitchResult(AgentSwitchResultMsg{ThreadID: threadID, Response: message.response})
	return m.refreshStatusControlsCmd()
}

// SetAgentsOverviewBlankSession records a started session whose live snapshot
// must be reused until its first turn materializes a rollout.
func (m *Model) setAgentsOverviewBlankSession(threadID string, response AgentThreadSwitchResponse) {
	if m == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	if m.agentsOverviewBlankSessions == nil {
		m.agentsOverviewBlankSessions = map[string]AgentThreadSwitchResponse{}
	}
	m.agentsOverviewBlankSessions[threadID] = response
}

// clearAgentsOverviewBlankSession drops a started session once it can be
// resumed normally: its first turn started, or it was closed/archived/deleted.
func (m *Model) clearAgentsOverviewBlankSession(threadID string) {
	if m == nil || len(m.agentsOverviewBlankSessions) == 0 {
		return
	}
	delete(m.agentsOverviewBlankSessions, strings.TrimSpace(threadID))
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

// openAgentsOverviewLifecycleConfirmation shows the archive/delete confirmation
// with Cancel selected by default (Rust #44433).
func (m *Model) openAgentsOverviewLifecycleConfirmation(action agentsOverviewLifecycleAction) {
	if m == nil || m.agentsOverview == nil || m.agentsOverviewBusy {
		return
	}
	threadID := m.agentsOverview.SelectedThreadID()
	if threadID == "" {
		return
	}
	name := "Untitled task"
	if row := m.agentsOverview.SelectedRow(); row != nil {
		name = row.Title()
	}
	title := "Archive \"" + name + "\"?"
	body := "This stops any running work in this task and its child agents, then archives them. Their history can be restored from the resume picker."
	confirmLabel := "Archive task and child agents"
	if action == agentsOverviewActionDelete {
		title = "Permanently delete \"" + name + "\"?"
		body = "This stops any running work in this task and its child agents, then permanently deletes their history. This cannot be undone."
		confirmLabel = "Permanently delete task and child agents"
	}
	m.agentsOverviewLifecycle = &agentsOverviewLifecycleRequest{threadID: threadID, action: action}
	m.openModal(ModalRequestMsg{
		ID:    agentsOverviewLifecycleModalID,
		Kind:  ModalKindAgents,
		Title: title,
		Body:  body,
		Options: []ModalOption{
			{ID: "cancel", Label: "Cancel", Description: "Keep this task"},
			// Rust #44744: archiving acts on the first confirmation; permanent
			// deletion keeps an explicit second confirmation.
			{ID: "confirm", Label: confirmLabel, Description: "Run the confirmed lifecycle action", RequireConfirmation: action == agentsOverviewActionDelete},
		},
	})
}

func (m *Model) applyAgentsOverviewLifecycleOption(optionID string) bubbletea.Cmd {
	request := m.agentsOverviewLifecycle
	m.agentsOverviewLifecycle = nil
	if request == nil {
		return nil
	}
	if optionID != "confirm" {
		m.notice = ""
		return nil
	}
	return m.runAgentsOverviewLifecycleCmd(request)
}

func (m *Model) runAgentsOverviewLifecycleCmd(request *agentsOverviewLifecycleRequest) bubbletea.Cmd {
	if m == nil || request == nil || m.agentsOverviewBusy || strings.TrimSpace(request.threadID) == "" {
		return nil
	}
	progress := "Archiving task\u2026"
	runAvailable := m.onAgentsOverviewArchive != nil
	if request.action == agentsOverviewActionDelete {
		progress = "Deleting task\u2026"
		runAvailable = m.onAgentsOverviewDelete != nil
	}
	if !runAvailable {
		m.agentsOverviewNotice = "The agents dashboard is unavailable in this runtime."
		return nil
	}
	m.agentsOverviewBusy = true
	m.agentsOverviewLifecycleProgress = progress
	m.agentsOverviewLifecycle = request
	action := request.action
	threadID := request.threadID
	return func() bubbletea.Msg {
		var err error
		if action == agentsOverviewActionDelete {
			err = m.onAgentsOverviewDelete(threadID)
		} else {
			err = m.onAgentsOverviewArchive(threadID)
		}
		return agentsOverviewLifecycleMsg{threadID: threadID, action: action, err: err}
	}
}

// applyAgentsOverviewLifecycleResult finishes an archive/delete RPC (Rust
// #44433): refresh the dashboard, keep it open when the current task was
// removed, and report failures without dropping the attachment or draft.
func (m *Model) applyAgentsOverviewLifecycleResult(msg agentsOverviewLifecycleMsg) bubbletea.Cmd {
	m.agentsOverviewLifecycleProgress = ""
	m.agentsOverviewBusy = false
	m.agentsOverviewLifecycle = nil
	if msg.err != nil {
		label := "archive"
		if msg.action == agentsOverviewActionDelete {
			label = "delete"
		}
		m.agentsOverviewNotice = "Failed to " + label + " task: " + strings.TrimSpace(msg.err.Error())
		return nil
	}
	m.agentsOverviewNotice = ""
	// Rust #45255: a removed task drops its retained (unmaterialized) session.
	m.clearAgentsOverviewBlankSession(msg.threadID)
	// Removing the current task leaves the dashboard open but unattached.
	if m.State != nil && strings.TrimSpace(m.State.ThreadID) == strings.TrimSpace(msg.threadID) {
		m.State.SetThreadID("")
		m.State.SetThreadName("")
	}
	if m.agentsOverview != nil {
		m.agentsOverview.UnhideThread(msg.threadID)
	}
	return m.refreshAgentsOverviewCmd()
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
	// Rust #44424: explicitly resuming a task clears its local hide.
	if m.agentsOverview != nil {
		m.agentsOverview.UnhideThread(threadID)
	}
	delete(m.agentsOverviewHidden, threadID)
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
	if blank, ok := m.agentsOverviewBlankSessions[threadID]; ok {
		// A session started from the command center has no rollout yet, so
		// thread/resume would fail; reuse its live snapshot instead (Rust #45255
		// agents_overview.blank_sessions).
		m.closeAgentsOverview()
		m.applyAgentSwitchResult(AgentSwitchResultMsg{ThreadID: threadID, Response: blank})
		return m.refreshStatusControlsCmd()
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
	if m.agentsOverview != nil {
		m.agentsOverviewHidden = m.agentsOverview.HiddenThreads()
	}
	m.agentsOverview = nil
	m.agentsOverviewNotice = ""
	m.agentsOverviewBusy = false
	m.agentsOverviewRefresh = 0
	m.agentsOverviewPending = false
	m.agentsOverviewInflight = false
	m.agentsOverviewLifecycle = nil
	m.agentsOverviewLifecycleProgress = ""
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
	if m.agentsOverviewLifecycleProgress != "" {
		lines = append(lines, "  "+m.agentsOverviewLifecycleProgress)
	}
	return strings.Join(lines, "\n")
}

// applyThreadScopedSettingsUpdated patches a listed task's model from a
// non-active thread's settings update (Rust #44957: the command center reflects
// a model change without waiting for the next thread-list refresh).
func (m *Model) applyThreadScopedSettingsUpdated(msg ThreadScopedSettingsUpdatedMsg) bubbletea.Cmd {
	if m == nil || m.agentsOverview == nil {
		return nil
	}
	if !m.agentsOverview.SetRowModel(msg.ThreadID, msg.Settings.Model) {
		return nil
	}
	m.refreshTranscript()
	return nil
}
