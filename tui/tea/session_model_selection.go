package tea

import (
	"fmt"
	"strings"

	codextui "codex_go/tui"
	"codex_go/tui/history_cell"
)

const (
	modelSessionSecondaryKey                  = "s"
	modelSessionFooterFormat                  = " \u00b7 "
	ultraReasoningConcurrencyWarningThreshold = 8
)

// listPrimaryHint renders the first configured binding for a list action the
// same way the picker footer does.
func (m *Model) listPrimaryHint(action string) string {
	if m == nil {
		return ""
	}
	bindings, _, _ := codextui.ResolvedKeymapBindings(m.keymapConfig, "list", action)
	for _, binding := range codextui.KeymapDisplayBindings(m.keymapConfig, bindings) {
		if value := strings.TrimSpace(binding); value != "" {
			return strings.ReplaceAll(value, "-", "+")
		}
	}
	return ""
}

// listKeymapClaimsBinding mirrors Rust's ListKeymap::action_for check: any
// configured list action owning the key suppresses an optional row shortcut.
func (m *Model) listKeymapClaimsBinding(binding string) bool {
	if m == nil || strings.TrimSpace(binding) == "" {
		return false
	}
	for _, descriptor := range codextui.KeymapActions(codextui.KeymapActionFilter{FastModeEnabled: true}) {
		if descriptor.Context != "list" {
			continue
		}
		if codextui.KeymapActionHasBinding(m.keymapConfig, "list", descriptor.Action, binding) {
			return true
		}
	}
	return false
}

func (m *Model) modelSessionSecondaryAvailable() bool {
	return m != nil && !m.listKeymapClaimsBinding(modelSessionSecondaryKey)
}

// modelSessionFooterHint mirrors Rust session_model_selection_action:
// `enter default/apply \u00b7 s session \u00b7 esc back`, using the active list
// keymap and shorter wording from the current upstream UI.
func (m *Model) modelSessionFooterHint(effort string) string {
	parts := make([]string, 0, 3)
	if accept := m.listPrimaryHint("accept"); accept != "" {
		label := " default"
		if strings.EqualFold(strings.TrimSpace(effort), "ultra") {
			label = " apply"
		}
		parts = append(parts, accept+label)
	}
	parts = append(parts, modelSessionSecondaryKey+" session")
	if cancel := m.listPrimaryHint("cancel"); cancel != "" {
		parts = append(parts, cancel+" back")
	}
	return strings.Join(parts, modelSessionFooterFormat)
}

func (m *Model) withSessionModelSecondary(option ModalOption, model string, effort string) ModalOption {
	if m == nil || strings.TrimSpace(model) == "" || model == LunaReserveModel || !m.modelSessionSecondaryAvailable() {
		return option
	}
	option.SecondaryShortcut = modelSessionSecondaryKey
	option.SecondaryFooterHint = m.modelSessionFooterHint(effort)
	return option
}

func (m *Model) modelPickerModalOption(option codextui.ModelPickerOption, index int) ModalOption {
	modalOption := ModalOption{
		ID:          option.ID,
		Label:       modelPickerLabel(option),
		Description: option.Description,
		Shortcut:    modelPickerShortcut(index),
	}
	if effort, ok := option.DirectReasoningEffort(); ok {
		modalOption = m.withSessionModelSecondary(modalOption, option.ID, effort)
	}
	return modalOption
}

func (m *Model) modelReasoningModalOptions(picker *codextui.ModelReasoningPicker) []ModalOption {
	if picker == nil {
		return nil
	}
	modalOptions := make([]ModalOption, 0, len(picker.Options))
	for i, effort := range picker.Options {
		label := effort.Label
		if label == "" {
			label = codextui.ReasoningEffortLabel(effort.Effort)
		}
		markers := []string{}
		if effort.IsCurrent {
			markers = append(markers, "current")
		}
		if effort.IsDefault {
			markers = append(markers, "default")
		}
		if len(markers) > 0 {
			label += " (" + strings.Join(markers, ", ") + ")"
		}
		modalOption := ModalOption{
			ID:          effort.Effort,
			Label:       label,
			Description: effort.Description,
			Shortcut:    modelPickerShortcut(i),
		}
		modalOptions = append(modalOptions, m.withSessionModelSecondary(modalOption, picker.Model.ID, effort.Effort))
	}
	return modalOptions
}

// ultraReasoningConcurrencyWarning mirrors Rust's
// ChatWidget::ultra_reasoning_concurrency_warning.
func (m *Model) ultraReasoningConcurrencyWarning(effort string) string {
	if m == nil || !strings.EqualFold(strings.TrimSpace(effort), "ultra") {
		return ""
	}
	maxThreads := m.multiAgentV2MaxConcurrentThreads
	if maxThreads < ultraReasoningConcurrencyWarningThreshold {
		return ""
	}
	maxSubagents := maxThreads - 1
	return fmt.Sprintf(
		"Ultra reasoning may proactively use multiple agents. This session is configured for %d concurrent threads with up to %d subagents which can increase usage quickly. Consider setting features.multi_agent_v2.max_concurrent_threads_per_session below 8.",
		maxThreads,
		maxSubagents,
	)
}

// resolveSessionModelPickerSelection applies a final picker choice to the
// active session only. It deliberately skips the Plan-scope submenu and
// never appends a ModelSelectionPersist entry (Rust #45831).
func (m *Model) resolveSessionModelPickerSelection(model string, effort string) (*PickerDecision, string, bool) {
	if m == nil || m.State == nil {
		return nil, "", true
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return nil, "", true
	}
	effort = strings.TrimSpace(effort)
	modelChanged := strings.TrimSpace(m.State.Model) != model
	if modelChanged {
		m.State.Model = model
		m.clearRateLimitRecoveryHold()
		m.refreshServiceTierCommands()
	}
	inPlanMode := m.State.PlanMode
	ultra := strings.EqualFold(effort, "ultra")
	if !inPlanMode || ultra {
		m.State.ReasoningEffort = effort
	}
	if inPlanMode || ultra {
		m.State.PlanModeReasoningEffort = effort
	}
	warning := m.ultraReasoningConcurrencyWarning(effort)
	message := modelCommandConfirmationMessage(modelCommandSelection{model: model, effort: effort})
	m.notice = message
	m.addInfoHistoryMessage(message)
	if warning != "" {
		m.applyHistoryCell(historycell.NewWarningEvent(warning))
	}
	m.refreshTranscript()
	return &PickerDecision{
		Kind:            "session_model",
		Value:           model,
		ReasoningEffort: effort,
	}, message, true
}
