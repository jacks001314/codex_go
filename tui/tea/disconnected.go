package tea

// Rust parity: codex-rs/tui/src/bottom_pane/chat_composer/reconnect.rs and
// app/event_dispatch.rs (#63258).
//
// A failed app-server transport leaves the conversation on screen. Local view
// commands keep working, and the slash popup explains which commands need the
// server instead of hiding them.

import (
	"strings"

	bubbletea "charm.land/bubbletea/v2"

	codextui "codex_go/tui"
	"codex_go/tui/chatwidget"
)

// setAppServerDisconnected enters or leaves the offline TUI state after the
// app-server transport failed or a fresh connection replaced it.
func (m *Model) setAppServerDisconnected(disconnected bool) {
	if m == nil {
		return
	}
	wasDisconnected := m.appServerDisconnected
	m.appServerDisconnected = disconnected
	if wasDisconnected == disconnected {
		return
	}
	// The command popup caches its catalog, so a connection change rebuilds it
	// while keeping the draft. Rust #63419 keeps the highlighted command when it
	// survives the new catalog and clears the selection when it disappears, so a
	// pending Enter cannot submit a different row than the user chose.
	active := m.slashPopup.Active
	previous := m.selectedSlashPopupName()
	m.slashPopup = slashCommandPopup{}
	if !active {
		if disconnected {
			m.refreshSlashPopup()
		}
		return
	}
	m.refreshSlashPopup()
	m.restoreSlashPopupSelection(previous)
}

// ReconnectAttemptFinishedMsg carries the outcome of a Ctrl+R reconnect
// attempt (Rust #63599 app/reconnect.rs retry_reconnect).
type ReconnectAttemptFinishedMsg struct {
	ThreadID string
	Response SessionResumeResponse
	Err      error
}

// canRetryReconnect reports whether Ctrl+R can start a fresh attempt: the
// transport is offline, no attempt is running, the displayed conversation has
// a thread to rejoin, and the backend is neither Cloud nor an in-process
// session (Rust #63599 can_retry_reconnect).
func (m *Model) canRetryReconnect() bool {
	if m == nil || m.State == nil || !m.appServerDisconnected || !m.reconnectFailed {
		return false
	}
	if m.serverOwnedSettings || m.onResumeSession == nil {
		return false
	}
	if m.reconnectAttemptThreadID != "" {
		return false
	}
	return strings.TrimSpace(m.State.ThreadID) != ""
}

// acceptsReconnectShortcut mirrors Rust's composer gate: the shortcut works
// outside Vim and in Vim Insert mode, so Normal mode redo stays intact.
func (m *Model) acceptsReconnectShortcut() bool {
	return m != nil && (!m.vimMode || m.vimInsert)
}

// setDisconnectedFooter shows the offline footer hints (Rust #63599
// set_disconnected_footer): retry plus quit when an attempt can start.
func (m *Model) setDisconnectedFooter(canRetry bool) {
	if m == nil {
		return
	}
	if !canRetry {
		m.footerHintOverride = "ctrl+c quit"
		return
	}
	retry := "ctrl+r reconnect"
	if m.vimMode {
		retry = "ctrl+r reconnect in Insert mode"
	}
	m.footerHintOverride = retry + " \u00b7 ctrl+c quit"
}

// showReconnecting paints the running attempt: a spinner row with a fresh
// start, the reconnecting footer, and no retry hint until it finishes
// (Rust #63599 show_reconnecting).
func (m *Model) showReconnecting() {
	if m == nil {
		return
	}
	m.reconnectFailed = false
	m.retryActivityMessage = "Reconnecting to server\u2026"
	m.retryActivityActive = true
	m.renderRetryActivity()
	m.setDisconnectedFooter(false)
	if m.statuslinePicker == nil || !m.statuslinePicker.offline {
		m.statuslineDisconnected = true
	}
}

// showReconnectFailed records a failed attempt and offers the retry hint
// again. The draft stays editable (Rust #63599 reconnect_failed).
func (m *Model) showReconnectFailed(canRetry bool) {
	if m == nil {
		return
	}
	m.reconnectFailed = true
	m.clearRetryActivity()
	m.setDisconnectedFooter(canRetry)
	if !canRetry {
		return
	}
	m.addInfoHistoryMessage("Connection to the server was lost. Check the server and press Ctrl+R to retry. Your draft is still editable. Copy it before quitting with Ctrl+C.")
	m.refreshTranscript()
}

// retryReconnect starts one fresh attempt by resuming the displayed
// conversation through a new connection (Rust #63599 retry_reconnect plus
// app/reconnect.rs, which reinitializes a client and rejoins the thread).
func (m *Model) retryReconnect() bubbletea.Cmd {
	if !m.canRetryReconnect() {
		return nil
	}
	threadID := strings.TrimSpace(m.State.ThreadID)
	cwd := strings.TrimSpace(m.State.CWD)
	m.reconnectAttemptThreadID = threadID
	m.showReconnecting()
	m.notice = "Reconnecting to server\u2026"
	m.refreshTranscript()
	resume := m.onResumeSession
	backendID := m.backendID
	return func() bubbletea.Msg {
		response, err := resume(codextui.SessionSelection{
			Kind:      codextui.SessionSelectionResume,
			Target:    codextui.SessionTarget{BackendID: backendID, ThreadID: threadID, CWD: cwd},
			ResumeCWD: cwd,
		})
		return ReconnectAttemptFinishedMsg{ThreadID: threadID, Response: response, Err: err}
	}
}

// applyReconnectAttemptFinished clears the offline state after a successful
// resume, or keeps the retry hint after a failure.
func (m *Model) applyReconnectAttemptFinished(msg ReconnectAttemptFinishedMsg) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	threadID := strings.TrimSpace(msg.ThreadID)
	if threadID == "" || threadID != strings.TrimSpace(m.reconnectAttemptThreadID) {
		return nil
	}
	m.reconnectAttemptThreadID = ""
	if msg.Err != nil {
		m.reconnectFailed = true
		m.clearRetryActivity()
		m.setDisconnectedFooter(true)
		text := strings.TrimSpace(msg.Err.Error())
		if text == "" {
			text = "unknown error"
		}
		m.notice = "Reconnect failed: " + text + ". Press Ctrl+R to retry."
		m.refreshTranscript()
		return nil
	}
	m.clearRetryActivity()
	m.reconnectFailed = false
	m.setAppServerDisconnected(false)
	m.footerHintOverride = ""
	m.statuslineDisconnected = false
	if state := m.agentsOverviewBackendState(m.backendID); state != nil {
		state.disconnected = false
	}
	if msg.Response.Summary != nil {
		m.upsertSessionItem(*msg.Response.Summary)
	}
	cwd := strings.TrimSpace(m.State.CWD)
	if msg.Response.Summary != nil && strings.TrimSpace(msg.Response.Summary.CWD) != "" {
		cwd = strings.TrimSpace(msg.Response.Summary.CWD)
	}
	m.applyResumeResponseWithCWD(threadID, msg.Response, cwd)
	m.notice = "Reconnected to the server."
	m.refreshTranscript()
	// Rust #63599 reestablishes the approval queue on the new connection;
	// the broker retired at disconnect time cannot deliver a decision.
	return bubbletea.Batch(m.refreshStatusControlsCmd(), m.reconnectThreadApprovalsCmd([]string{threadID}))
}

// clearStaleMCPStartupOnDisconnect drops the in-flight MCP startup round
// (Rust #63599 pause_for_disconnect: mcp_startup_status.take()). Startup
// notifications from the disconnected transport can no longer finish, so they
// must not keep local commands busy; a real running turn keeps its own busy
// state.
func (m *Model) clearStaleMCPStartupOnDisconnect() {
	if m == nil || !m.mcpStartupActive {
		return
	}
	m.mcpStartup = chatwidget.NewMcpStartupRoundState()
	m.mcpStartupActive = false
	m.mcpStartupHeader = ""
}

// firstLineOfText returns everything before the first newline.
func firstLineOfText(text string) string {
	if index := strings.IndexByte(text, '\n'); index >= 0 {
		return text[:index]
	}
	return text
}

// disconnectedSlashCommand reports whether the draft is a command the TUI can
// run without its app server. The command must occupy one line and be complete,
// mirroring Rust's bare_command/inline_command matching.
func disconnectedSlashCommand(input string) bool {
	raw := input
	input = strings.TrimSpace(input)
	if input == "" || raw != input || strings.Contains(input, "\n") {
		return false
	}
	invocation, ok := codextui.ParseCommand(input)
	if !ok || !slashInvocationDispatchable(invocation) {
		return false
	}
	return invocation.Command.AvailableWhenDisconnected()
}

// offlineFeedbackMessage reports the thread identity and the reconnect guidance
// /feedback shows while the server is unreachable.
func (m *Model) offlineFeedbackMessage() (string, string) {
	thread := "Thread ID unavailable."
	if m != nil {
		if id := strings.TrimSpace(m.State.ThreadID); id != "" {
			thread = "Thread ID " + id
		}
	}
	return thread, "Reconnect and retry /feedback."
}

// noteOfflineConnectionRequirement explains a command the offline TUI cannot run
// and keeps the draft intact.
func (m *Model) noteOfflineConnectionRequirement(command string) {
	if m == nil {
		return
	}
	name := strings.TrimSpace(command)
	if name == "" {
		name = "That command"
	} else if !strings.HasPrefix(name, "/") {
		name = "/" + name
	}
	m.notice = name + " requires a server connection."
	m.refreshTranscript()
}
