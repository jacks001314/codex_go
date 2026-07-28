package tea

import (
	"errors"
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	historycell "codex_go/tui/history_cell"
)

func (m *Model) startCompaction() bubbletea.Cmd {
	if m == nil || m.State == nil {
		return nil
	}
	threadID := strings.TrimSpace(m.State.ThreadID)
	if threadID == "" {
		m.applyHistoryCell(historycell.NewErrorEvent("'/compact' is unavailable before the session starts."))
		m.notice = "Compaction unavailable"
		return nil
	}
	if m.onStartCompactCommand == nil {
		return m.applyCompactStartResult(CompactStartResultMsg{Err: errors.New("compaction runtime is unavailable")})
	}
	m.State.TotalTokenUsage = codextui.TokenUsage{}
	m.State.LastTokenUsage = codextui.TokenUsage{}
	m.State.ModelContextWindow = nil
	m.setStatus("running")
	m.notice = ""
	m.refreshTranscript()
	return m.onStartCompactCommand(threadID)
}

func (m *Model) applyCompactStartResult(message CompactStartResultMsg) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	m.clearRetryActivity()
	m.setStatus("idle")
	if message.Err != nil {
		m.applyHistoryCell(historycell.NewErrorEvent("Compaction: " + strings.TrimSpace(message.Err.Error())))
		m.notice = "Compaction failed"
	} else {
		m.notice = ""
	}
	m.refreshTranscript()
	return bubbletea.Batch(m.refreshStatusControlsCmd(), m.submitNextQueued())
}
