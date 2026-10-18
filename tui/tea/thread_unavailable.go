package tea

import (
	"strings"

	codextui "codex_go/tui"
)

// setThreadUnavailable enters or leaves Rust #46566's restricted input mode.
// A read-only thread owned by another app takes precedence.
func (m *Model) setThreadUnavailable(unavailable bool) {
	if m == nil {
		return
	}
	m.threadUnavailable = unavailable && !m.readOnlyThread
	if !m.threadUnavailable {
		return
	}
	// Cached review and MCP startup activity must not keep recovery commands
	// blocked on a replay-only thread.
	m.reviewState.IsReviewMode = false
	m.currentTurnID = ""
	m.slashPopup = slashCommandPopup{}
	m.skillPopup = skillPopupState{}
	m.mentionPopup = nil
	m.mcpStartupActive = false
	m.mcpStartupHeader = ""
	m.mcpStartupFinishPending = false
	m.setStatus("idle")
	m.refreshTranscript()
}

// unavailableThreadCommand reports whether input may run while the current
// thread is unavailable. The command must occupy one line.
func unavailableThreadCommand(input string) bool {
	input = strings.TrimSpace(input)
	if input == "" || strings.Contains(input, "\n") {
		return false
	}
	invocation, ok := codextui.ParseCommand(input)
	if !ok {
		return false
	}
	switch invocation.Command {
	case codextui.CommandNew,
		codextui.CommandClear,
		codextui.CommandResume,
		codextui.CommandAgents,
		codextui.CommandAgent,
		codextui.CommandExit,
		codextui.CommandStatus,
		codextui.CommandDebugConfig,
		codextui.CommandPwd,
		codextui.CommandRollout,
		codextui.CommandCopy,
		codextui.CommandRaw:
		return true
	default:
		return false
	}
}
