package tea

import (
	"fmt"
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
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
	notice := m.cyberPolicyNotice()
	m.applyHistoryCell(historycell.NewCyberPolicyErrorEvent(notice))
	if supported, _ := m.daybreakModelSupport(); supported && m.daybreakAccountEligible() &&
		!m.daybreakEnabled && m.activeSide == nil && strings.TrimSpace(m.currentThreadID()) != "" {
		m.openSelectionViewModal(ModalKindGeneric, chatwidget.DaybreakEnableView())
	}
	return nil
}

func (m *Model) cyberPolicyNotice() codextui.DaybreakNotice {
	if m == nil || m.State == nil || !m.State.HasChatGPTAccount {
		return codextui.DaybreakNoticeApply
	}
	provider := strings.ToLower(strings.TrimSpace(m.State.Provider))
	if provider != "" && provider != "openai" {
		return codextui.DaybreakNoticeApply
	}
	option, ok := m.currentModelCatalogOption(strings.TrimSpace(m.State.Model))
	if !ok {
		return codextui.DaybreakNoticeApply
	}
	return codextui.DaybreakNoticeFromCyberPrograms(option.ID, option.AvailableCyberPrograms)
}

func (m *Model) currentModelCatalogOption(modelID string) (codextui.ModelPickerOption, bool) {
	if m == nil || strings.TrimSpace(modelID) == "" {
		return codextui.ModelPickerOption{}, false
	}
	for _, options := range [][]codextui.ModelPickerOption{m.modelCatalogOpts, m.modelPickerOpts} {
		for _, option := range options {
			if strings.EqualFold(strings.TrimSpace(option.ID), modelID) {
				return option, true
			}
		}
	}
	return codextui.ModelPickerOption{}, false
}

// daybreakModelSupport reports whether the current catalog advertises Daybreak.
// A nil catalog is unknown; an empty/advertised list without daybreak_blue is
// definitely unavailable.
func (m *Model) daybreakModelSupport() (supported bool, known bool) {
	if m == nil || m.State == nil {
		return false, false
	}
	modelID := strings.TrimSpace(m.State.Model)
	option, ok := m.currentModelCatalogOption(modelID)
	if !ok {
		return false, false
	}
	if len(option.AvailableCyberPrograms) == 0 {
		return false, false
	}
	for _, program := range option.AvailableCyberPrograms {
		if strings.EqualFold(strings.TrimSpace(program), "daybreak_blue") {
			return true, true
		}
	}
	return false, true
}

// applyDaybreakCommand toggles the thread's Daybreak preference and persists it
// without starting a turn (Rust SlashCommand::Daybreak).
func (m *Model) applyDaybreakCommand() bubbletea.Cmd {
	if m == nil || m.State == nil {
		return nil
	}
	if !m.daybreakFeatureEnabled() {
		m.addErrorHistoryMessage("Daybreak is unavailable.")
		return nil
	}
	if !m.daybreakEnabled {
		if !m.daybreakAccountEligible() {
			m.addErrorHistoryMessage("Daybreak requires the OpenAI provider with either ChatGPT sign-in or an API key with Daybreak support enabled.")
			return nil
		}
		if supported, known := m.daybreakModelSupport(); !supported {
			if known {
				m.addErrorHistoryMessage("Daybreak is not available for this account. Apply at https://openai.com/form/enterprise-trusted-access-for-cyber/. Learn more at https://help.openai.com/en/articles/20001326.")
			} else {
				m.addErrorHistoryMessage("Daybreak availability could not be determined from the connected host's model catalog.")
			}
			return nil
		}
	}
	threadID := strings.TrimSpace(m.currentThreadID())
	if threadID == "" {
		m.addErrorHistoryMessage("Daybreak is unavailable until the thread starts.")
		return nil
	}
	return m.setDaybreakPreference(!m.daybreakEnabled)
}

// setDaybreakPreference persists and applies one Daybreak selection. It never
// retries the refused request.
func (m *Model) setDaybreakPreference(enabled bool) bubbletea.Cmd {
	if m == nil || m.State == nil {
		return nil
	}
	threadID := strings.TrimSpace(m.currentThreadID())
	if threadID == "" {
		m.addErrorHistoryMessage("Daybreak is unavailable until the thread starts.")
		return nil
	}
	if m.onUpdateDaybreak != nil {
		if err := m.onUpdateDaybreak(threadID, enabled); err != nil {
			m.addErrorHistoryMessage(fmt.Sprintf("Failed to save Daybreak preference: %v", err))
			return nil
		}
	}
	m.daybreakEnabled = enabled
	if enabled {
		m.notice = "Daybreak is on for new turns."
	} else {
		m.notice = "Daybreak is off for new turns."
	}
	m.refreshTranscript()
	return m.refreshStatusControlsCmd()
}
