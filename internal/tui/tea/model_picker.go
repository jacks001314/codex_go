package tea

import (
	"strings"

	codextui "codex_go/internal/tui"
)

func (m *Model) openModelPicker() {
	options := append([]codextui.ModelPickerOption(nil), m.modelPickerOpts...)
	if len(options) == 0 {
		options = codextui.BundledModelPickerOptions()
	}
	picker := codextui.NewModelPicker(options, m.State.Model)
	if picker == nil || len(picker.Options) == 0 {
		m.notice = "No models are available right now."
		return
	}
	modalOptions := make([]ModalOption, 0, len(picker.Options))
	for i, option := range picker.Options {
		modalOptions = append(modalOptions, ModalOption{
			ID:          option.ID,
			Label:       modelPickerLabel(option),
			Description: option.Description,
			Shortcut:    modelPickerShortcut(i),
		})
	}
	m.openModal(ModalRequestMsg{
		ID:      "model-picker",
		Kind:    ModalKindPicker,
		Title:   "Select Model",
		Body:    "Pick the model for following turns.",
		Options: modalOptions,
	})
	if m.modal != nil {
		m.modal.modelPicker = picker
		m.modal.selected = picker.Selected
	}
}

func (m *Model) openModelReasoningPicker(option codextui.ModelPickerOption) {
	picker := codextui.NewModelReasoningPicker(option, m.State.EffectiveReasoningEffort())
	if picker == nil || len(picker.Options) == 0 {
		m.State.Model = option.ID
		m.notice = strings.TrimSpace(m.State.RenderSetting("Model", m.State.Model))
		return
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
		modalOptions = append(modalOptions, ModalOption{
			ID:          effort.Effort,
			Label:       label,
			Description: effort.Description,
			Shortcut:    modelPickerShortcut(i),
		})
	}
	m.openModal(ModalRequestMsg{
		ID:      "model-reasoning-picker",
		Kind:    ModalKindPicker,
		Title:   "Select Reasoning Level for " + option.ID,
		Body:    "Pick the reasoning effort for following turns.",
		Options: modalOptions,
	})
	if m.modal != nil {
		m.modal.modelReasoning = picker
		m.modal.selected = picker.Selected
	}
}

func (m *Model) openPlanReasoningScopePicker(option codextui.ModelPickerOption, effort string) {
	picker := codextui.NewPlanReasoningScopePicker(option, effort, m.State.PlanModeReasoningEffort)
	if picker == nil || len(picker.Options) == 0 {
		m.State.Model = option.ID
		m.State.PlanModeReasoningEffort = strings.TrimSpace(effort)
		m.notice = strings.TrimSpace(m.State.RenderSetting("Plan Reasoning", m.State.PlanModeReasoningEffort))
		return
	}
	modalOptions := make([]ModalOption, 0, len(picker.Options))
	for i, option := range picker.Options {
		modalOptions = append(modalOptions, ModalOption{
			ID:          string(option.Scope),
			Label:       option.Label,
			Description: option.Description,
			Shortcut:    modelPickerShortcut(i),
		})
	}
	body := "Choose where to apply " + codextui.ReasoningEffortLabel(effort) + " reasoning."
	if strings.TrimSpace(effort) == "" {
		body = "Choose where to apply the selected reasoning."
	}
	m.openModal(ModalRequestMsg{
		ID:      "plan-reasoning-scope-picker",
		Kind:    ModalKindPicker,
		Title:   codextui.PlanReasoningScopeTitle,
		Body:    body,
		Options: modalOptions,
	})
	if m.modal != nil {
		m.modal.planReasoningScope = picker
		m.modal.selected = picker.Selected
	}
}

func modelPickerLabel(option codextui.ModelPickerOption) string {
	label := strings.TrimSpace(option.Label)
	if label == "" {
		label = option.ID
	}
	markers := []string{}
	if option.IsCurrent {
		markers = append(markers, "current")
	}
	if option.IsDefault {
		markers = append(markers, "default")
	}
	if len(markers) > 0 {
		label += " (" + strings.Join(markers, ", ") + ")"
	}
	return label
}

func modelPickerShortcut(index int) string {
	if index >= 0 && index < 9 {
		return string(rune('1' + index))
	}
	return ""
}
