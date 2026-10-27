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
	// TurnID scopes the block to the turn that produced it (Rust
	// ErrorNotification.turn_id). Empty accepts the active turn.
	TurnID string
	// Message is the server's refusal text, used to mark running tool calls
	// failed when a recourse block replaces the generic error cell.
	Message string
}

// applyCyberPolicyErrorMsg renders the cyber refusal cell for the active thread.
func (m *Model) applyCyberPolicyErrorMsg(msg CyberPolicyErrorMsg) bubbletea.Cmd {
	if m == nil || m.State == nil {
		return nil
	}
	if threadID := strings.TrimSpace(msg.ThreadID); threadID != "" && threadID != m.currentThreadID() {
		return nil
	}
	// Rust #57363: a pending cyber block renders its turn-scoped explanation and
	// safer rewrites instead of the plain refusal cell.
	if m.applyCyberPolicyRecourse(msg) {
		return nil
	}
	m.clearSafetyRecourse()
	notice := m.cyberPolicyNotice()
	m.applyHistoryCell(historycell.NewCyberPolicyErrorEvent(notice))
	if supported, _ := m.daybreakModelSupport(); supported && m.daybreakAccountEligible() &&
		!m.daybreakEnabled && m.activeSide == nil && strings.TrimSpace(m.currentThreadID()) != "" {
		m.openSelectionViewModal(ModalKindGeneric, chatwidget.DaybreakEnableView())
	}
	return nil
}

func (m *Model) cyberPolicyNotice() codextui.DaybreakNotice {
	if m == nil || m.State == nil {
		return codextui.DaybreakNoticeApply
	}
	// Rust ChatWidget::cyber_policy_notice: `enabled` only counts while the
	// account can use Daybreak and no side conversation is active.
	canEnable := m.daybreakAccountEligible() && m.activeSide == nil
	enabled := m.daybreakEnabled && canEnable
	modelID := strings.TrimSpace(m.State.Model)
	option, ok := m.currentModelCatalogOption(modelID)
	if !ok {
		return codextui.DaybreakNoticeForSetting(modelID, nil, enabled, canEnable)
	}
	return codextui.DaybreakNoticeForSetting(option.ID, option.AvailableCyberPrograms, enabled, canEnable)
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
	if option.AvailableCyberPrograms == nil {
		return false, false
	}
	return daybreakProgram(option.AvailableCyberPrograms) != "", true
}

// daybreakProgram selects the catalog's preferred Daybreak program. Rust
// ModelAccessPrograms::daybreak prefers daybreak_blue and falls back to
// daybreak_red.
func daybreakProgram(programs []string) string {
	for _, want := range []string{"daybreak_blue", "daybreak_red"} {
		for _, program := range programs {
			if strings.EqualFold(strings.TrimSpace(program), want) {
				return want
			}
		}
	}
	return ""
}

// daybreakCatalogAvailability mirrors Rust daybreak::availability: any model
// advertising Daybreak proves availability, while absence is conclusive only
// when every catalog entry carries program metadata.
func (m *Model) daybreakCatalogAvailability() (available bool, known bool) {
	if m == nil {
		return false, false
	}
	options := m.modelCatalogOpts
	if len(options) == 0 {
		options = m.modelPickerOpts
	}
	if len(options) == 0 {
		return false, false
	}
	allKnown := true
	for _, option := range options {
		if daybreakProgram(option.AvailableCyberPrograms) != "" {
			return true, true
		}
		if option.AvailableCyberPrograms == nil {
			allKnown = false
		}
	}
	return false, allKnown
}

// daybreakCommandDescription mirrors Rust ChatWidget::daybreak_command_description.
// The bool is false when the /daybreak row must be hidden.
func (m *Model) daybreakCommandDescription() (string, bool) {
	if m == nil {
		return "", false
	}
	if m.daybreakEnabled && m.daybreakFeatureEnabled() {
		return "Disable broader access for cybersecurity work", true
	}
	if !m.daybreakAccountEligible() {
		return "", false
	}
	available, known := m.daybreakCatalogAvailability()
	switch {
	case available:
		return "Enable broader access for cybersecurity work", true
	case known:
		return "Learn about broader access for cybersecurity work", true
	default:
		return "Manage broader access for cybersecurity work", true
	}
}

// applyDaybreakCommand toggles the thread's Daybreak preference and persists it
// without starting a turn (Rust SlashCommand::Daybreak).
func (m *Model) applyDaybreakCommand() bubbletea.Cmd {
	if m == nil || m.State == nil {
		return nil
	}
	if !m.daybreakFeatureEnabled() {
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
	m.daybreakEnabled = enabled && m.daybreakFeatureEnabled()
	if enabled {
		m.notice = "Daybreak is on for new turns."
	} else {
		m.notice = "Daybreak is off for new turns."
	}
	m.refreshTranscript()
	return m.refreshStatusControlsCmd()
}

// syncDaybreakFeatureGate re-applies cli_daybreak to the live thread
// preference after a feature toggle or configuration reload (Rust
// ChatWidget::set_daybreak_enabled).
func (m *Model) syncDaybreakFeatureGate() {
	if m == nil {
		return
	}
	m.daybreakEnabled = m.daybreakEnabled && m.daybreakFeatureEnabled()
}
