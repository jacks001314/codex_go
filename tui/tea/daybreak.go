package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	historycell "codex_go/tui/history_cell"
)

// CyberPolicyErrorMsg reports a turn the server blocked for cybersecurity
// reasons, so the TUI can render the Daybreak-aware refusal cell instead of the
// generic turn error (Rust chatwidget::on_cyber_policy_error).
type CyberPolicyErrorMsg struct {
	ThreadID string
}

// applyCyberPolicyErrorMsg renders the cyber refusal cell for the active thread.
func (m *Model) applyCyberPolicyErrorMsg(msg CyberPolicyErrorMsg) bubbletea.Cmd {
	if m == nil || m.State == nil {
		return nil
	}
	if threadID := strings.TrimSpace(msg.ThreadID); threadID != "" && threadID != m.currentThreadID() {
		return nil
	}
	notice := codextui.DaybreakNoticeLimited
	if m.onDaybreakNotice != nil {
		notice = m.onDaybreakNotice(strings.TrimSpace(m.State.Model))
	}
	m.applyHistoryCell(historycell.NewCyberPolicyErrorEvent(notice))
	return nil
}
