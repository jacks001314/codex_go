package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
)

func (m *Model) maybeOpenRateLimitSwitchPrompt(snapshot chatwidget.RateLimitSnapshot) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	currentModel := ""
	if m.State != nil {
		currentModel = m.State.Model
	}
	if chatwidget.ShouldQueueRateLimitSwitchPrompt(snapshot, currentModel, m.hideRateLimitModelNudge, m.rateLimitSwitchPrompt) {
		m.rateLimitSwitchPrompt = chatwidget.RateLimitSwitchPromptPending
	}
	if m.hideRateLimitModelNudge {
		m.rateLimitSwitchPrompt = chatwidget.RateLimitSwitchPromptIdle
		return nil
	}
	if m.rateLimitSwitchPrompt != chatwidget.RateLimitSwitchPromptPending {
		return nil
	}
	option, ok := m.lowerCostModelOption()
	if !ok {
		m.rateLimitSwitchPrompt = chatwidget.RateLimitSwitchPromptIdle
		return nil
	}
	m.openRateLimitSwitchPrompt(option)
	m.rateLimitSwitchPrompt = chatwidget.RateLimitSwitchPromptShown
	return nil
}

func (m *Model) lowerCostModelOption() (codextui.ModelPickerOption, bool) {
	if m == nil {
		return codextui.ModelPickerOption{}, false
	}
	options := append([]codextui.ModelPickerOption(nil), m.modelPickerOpts...)
	if len(options) == 0 {
		options = codextui.BundledModelPickerOptions()
	}
	for _, option := range options {
		if strings.TrimSpace(option.ID) == chatwidget.NudgeModelSlug {
			return option, true
		}
	}
	return codextui.ModelPickerOption{}, false
}

func (m *Model) openRateLimitSwitchPrompt(option codextui.ModelPickerOption) {
	if m == nil {
		return
	}
	reasoning := option.DefaultReasoning()
	m.rateLimitSwitchModel = strings.TrimSpace(option.ID)
	m.rateLimitSwitchReasoning = strings.TrimSpace(reasoning)
	view := chatwidget.NewRateLimitSwitchPromptView(chatwidget.RateLimitSwitchPreset{
		Model:                  option.ID,
		Description:            option.Description,
		DefaultReasoningEffort: reasoning,
	})
	m.openSelectionViewModal(ModalKindRateLimitSwitch, view)
}

func (m *Model) applyRateLimitSwitchModalOption(optionID string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	switch strings.TrimSpace(optionID) {
	case "switch":
		return m.switchToRateLimitNudgeModel()
	case "hide":
		m.hideRateLimitModelNudge = true
		m.rateLimitSwitchPrompt = chatwidget.RateLimitSwitchPromptIdle
		m.notice = "Rate limit model switch reminders hidden."
		m.refreshTranscript()
		if m.onWriteSettings != nil {
			hidden := true
			return m.writeSettings(settingsWriteKindRateLimitModelNudge, []SettingsEdit{{
				KeyPath: "notices.hide_rate_limit_model_nudge",
				Value:   hidden,
			}})
		}
		return nil
	default:
		m.notice = "Keeping current model."
		m.refreshTranscript()
		return nil
	}
}

func (m *Model) switchToRateLimitNudgeModel() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	model := strings.TrimSpace(m.rateLimitSwitchModel)
	if model == "" {
		model = chatwidget.NudgeModelSlug
	}
	reasoning := strings.TrimSpace(m.rateLimitSwitchReasoning)
	if m.State != nil {
		m.State.Model = model
		if reasoning != "" {
			m.State.ReasoningEffort = reasoning
		}
		m.notice = strings.TrimSpace(m.State.RenderSetting("Model", m.State.Model))
		if reasoning != "" {
			m.notice += "\n" + strings.TrimSpace(m.State.RenderSetting("Reasoning", m.State.ReasoningEffort))
		}
	} else {
		m.notice = "Model: " + model
	}
	m.refreshTranscript()
	cmds := []bubbletea.Cmd{m.refreshStatusControlsCmd()}
	if m.onModalResponse != nil {
		effectiveReasoning := reasoning
		if m.State != nil {
			effectiveReasoning = m.State.ReasoningEffort
		}
		cmds = append(cmds, m.onModalResponse(ModalResponse{
			ID:       chatwidget.RateLimitSwitchPromptViewID,
			Kind:     ModalKindRateLimitSwitch,
			OptionID: "switch",
			Picker: &PickerDecision{
				Kind:            "rate_limit_switch_model",
				Value:           model,
				ReasoningEffort: effectiveReasoning,
			},
		}))
	}
	return bubbletea.Batch(cmds...)
}
