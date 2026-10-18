package tea

import (
	"strings"

	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
	historycell "codex_go/tui/history_cell"
)

// appendDaybreakNewTurnNotice mirrors Rust #54097: the notice follows the
// session header and appears once when a Daybreak-enabled thread starts.
func (m *Model) appendDaybreakNewTurnNotice() {
	if m == nil || m.State == nil || !m.daybreakEnabled {
		return
	}
	if m.hasDaybreakNewTurnNotice() {
		return
	}
	m.applyHistoryCell(historycell.NewInfoEvent(chatwidget.DaybreakNewTurnsNotice, ""))
}

// prependDaybreakNewTurnNotice places the notice after the configured header
// and before replayed transcript messages.
func (m *Model) prependDaybreakNewTurnNotice() {
	if m == nil || m.State == nil || !m.daybreakEnabled || m.hasDaybreakNewTurnNotice() {
		return
	}
	width := max(m.width, 20)
	cell := historycell.NewInfoEvent(chatwidget.DaybreakNewTurnsNotice, "")
	index := 0
	if m.sessionHeaderShown {
		index = 1
	}
	m.insertHistoryMessageAt(index, cell.DisplayLines(width), cell.RawLines())
}

func (m *Model) hasDaybreakNewTurnNotice() bool {
	if m == nil || m.State == nil {
		return false
	}
	for _, message := range m.State.Messages {
		if message.Role == codextui.RoleHistory &&
			strings.Contains(message.Text, chatwidget.DaybreakNewTurnsNotice) {
			return true
		}
	}
	return false
}
