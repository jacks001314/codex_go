package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	chatwidget "codex_go/tui/chatwidget"
	historycell "codex_go/tui/history_cell"
)

func (m *Model) openAutoReviewDenials() {
	if m == nil {
		return
	}
	threadID := ""
	if m.State != nil {
		threadID = strings.TrimSpace(m.State.ThreadID)
	}
	result := chatwidget.BuildAutoReviewDenialsPopup(threadID, m.toolRequestRuntime.RecentAutoReviewDenials)
	switch result.Outcome {
	case chatwidget.AutoReviewDenialsPopupShow:
		m.openSelectionViewModal(ModalKindAutoReview, result.View)
	case chatwidget.AutoReviewDenialsPopupError:
		m.applyHistoryCell(historycell.NewErrorEvent(result.Message))
	default:
		m.applyHistoryCell(historycell.NewInfoEvent(result.Message, result.Hint))
	}
}

func (m *Model) applyAutoReviewDenialSelection(id string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if m.onApproveAutoReviewDenial == nil {
		m.applyHistoryCell(historycell.NewErrorEvent("Auto-review approval is unavailable in this runtime."))
		return nil
	}
	state := chatwidget.AutoReviewDenialsState{Entries: m.toolRequestRuntime.RecentAutoReviewDenials}
	result := chatwidget.ApproveRecentAutoReviewDenial(&state, id)
	m.toolRequestRuntime.RecentAutoReviewDenials = state.Entries
	if result.Error != "" {
		m.applyHistoryCell(historycell.NewErrorEvent(result.Error))
		return nil
	}
	m.applyHistoryCell(historycell.NewInfoEvent(result.Message, result.Hint))
	threadID := ""
	if m.State != nil {
		threadID = strings.TrimSpace(m.State.ThreadID)
	}
	entry := result.Entry
	return func() bubbletea.Msg {
		err := m.onApproveAutoReviewDenial(threadID, entry)
		return AutoReviewDenialApproveResultMsg{Entry: entry, Err: err}
	}
}

func (m *Model) applyAutoReviewDenialApproveResult(message AutoReviewDenialApproveResultMsg) {
	if m == nil || message.Err == nil {
		return
	}
	m.applyHistoryCell(historycell.NewErrorEvent("Failed to approve auto-review denial: " + strings.TrimSpace(message.Err.Error())))
}

func (m *Model) applyGuardianReview(message GuardianReviewMsg) {
	if m == nil {
		return
	}
	if m.State != nil {
		currentThreadID := strings.TrimSpace(m.State.ThreadID)
		messageThreadID := strings.TrimSpace(message.ThreadID)
		if currentThreadID != "" && messageThreadID != "" && currentThreadID != messageThreadID {
			return
		}
	}
	result := m.toolRequestRuntime.OnGuardianAssessment(message.Event)
	if strings.TrimSpace(result.HistoryMessage) != "" {
		m.applyHistoryCell(historycell.NewInfoEvent(result.HistoryMessage, ""))
	}
}
