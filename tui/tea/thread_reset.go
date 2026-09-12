package tea

import (
	chatwidget "codex_go/tui/chatwidget"
)

// resetThreadScopedState drops transcript state owned by the previous thread so
// history or tool events queued by it cannot repopulate the new thread's replay
// (Rust #43994 reset_for_thread_switch). The composer draft, pending async
// questions, queued follow-ups, and user settings are deliberately preserved.
func (m *Model) resetThreadScopedState() {
	if m == nil {
		return
	}
	// The model carries its own tool-call display maps (shadowing the embedded
	// TranscriptComponent's copies), so clear both.
	m.toolCalls = map[string]*toolCallDisplayState{}
	m.mcpToolCalls = map[string]*mcpToolCallDisplayState{}
	m.Transcript.toolCalls = map[string]*toolCallDisplayState{}
	m.Transcript.mcpToolCalls = map[string]*mcpToolCallDisplayState{}
	m.startedThreadIDs = map[string]bool{}
	m.completedThreadIDs = map[string]bool{}
	m.Transcript.startedThreadIDs = map[string]bool{}
	m.Transcript.completedThreadIDs = map[string]bool{}
	m.lastTurnError = ""
	m.needsFinalMessageSeparator = false
	m.activeAssistantDeltaItemID = ""
	m.Transcript.lastTurnError = ""
	m.Transcript.needsFinalMessageSeparator = false
	m.Transcript.activeAssistantDeltaItemID = ""
	m.renderedFileChanges = map[string]bool{}
	m.retryMessageIndex = -1
	m.retryActivityActive = false
	m.retryActivityMessage = ""
	m.clearCompactionActivity()
	m.toolRequestRuntime = chatwidget.ToolRequestRuntimeState{}
	m.resetReviewModeState()
	m.pendingSteers = nil
	m.rejectedSteers = nil
	// The transcript overlay is a snapshot of the previous thread; leaving it
	// open would keep stale content on screen (Rust clears the overlay too).
	if m.overlay != nil || m.overlayAltScreen {
		m.overlay = nil
		m.overlayTranscript = false
		m.overlayAltScreen = false
		m.setAlternateScroll(false)
	}
}
