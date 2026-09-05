package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/tui/chatwidget"
)

// applySafetyBufferingUpdate renders the model/safetyBuffering/updated
// notification. The first eligible update shows the buffering prompt; further
// updates refresh state without showing the prompt twice (Rust #42380
// safety_buffering.rs).
func (m *Model) applySafetyBufferingUpdate(msg ModelSafetyBufferingMsg) {
	if m == nil {
		return
	}
	turnID := strings.TrimSpace(msg.TurnID)
	if turnID == "" {
		return
	}
	if !msg.ShowBufferingUI {
		m.clearSafetyBuffering()
		return
	}
	m.safetyBuffering.RecordTurn(turnID)
	fasterModel := ""
	if msg.FasterModel != nil {
		fasterModel = strings.TrimSpace(*msg.FasterModel)
	}
	result := m.safetyBuffering.ApplyWithContext(
		chatwidget.SafetyBufferingUpdate{
			TurnID:          turnID,
			ShowBufferingUI: true,
			FasterModel:     fasterModel,
			CanRetry:        fasterModel != "",
		},
		chatwidget.SafetyBufferingContext{
			ThreadID:    strings.TrimSpace(m.State.ThreadID),
			EnforceTurn: false,
		},
	)
	if result.Prompt != nil {
		m.safetyBufferingThreadID = strings.TrimSpace(msg.ThreadID)
		m.safetyBufferingFasterModel = fasterModel
		m.openSelectionViewModal(ModalKindGeneric, *result.Prompt)
		return
	}
	if result.Cleared {
		m.clearSafetyBuffering()
	}
}

// clearSafetyBuffering dismisses the buffering/confirmation view and drops the
// active buffering state.
func (m *Model) clearSafetyBuffering() {
	if m == nil {
		return
	}
	m.safetyBuffering.Clear()
	m.safetyBufferingThreadID = ""
	m.safetyBufferingFasterModel = ""
	if m.modal != nil && m.modal.id == chatwidget.SafetyBufferingPromptViewID {
		m.modal = nil
	}
}

// applySafetyBufferingModalOption handles choices from the buffering prompt
// ("retry" opens the Rust #42380 confirmation) and the confirmation itself
// ("wait" keeps waiting, "stop-retry" performs the confirmed retry).
func (m *Model) applySafetyBufferingModalOption(optionID string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	switch strings.TrimSpace(optionID) {
	case "retry":
		m.openSelectionViewModal(ModalKindGeneric, chatwidget.NewSafetyBufferingConfirmationView(m.safetyBufferingFasterModel))
		return nil
	case "stop-retry":
		threadID := m.safetyBufferingThreadID
		turnID := strings.TrimSpace(m.safetyBuffering.ActiveTurnID)
		model := m.safetyBufferingFasterModel
		m.clearSafetyBuffering()
		if m.onSafetyBufferingRetry == nil {
			m.notice = "Safety retry is unavailable; Codex will keep waiting."
			m.addInfoHistoryMessage(m.notice)
			m.refreshTranscript()
			return nil
		}
		return m.onSafetyBufferingRetry(threadID, turnID, model)
	default:
		m.clearSafetyBuffering()
		return nil
	}
}
