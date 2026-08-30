package tea

import (
	"strings"

	codextui "codex_go/tui"
	bubbletea "github.com/charmbracelet/bubbletea"
)

func (m *Model) openModelPicker() bubbletea.Cmd {
	options := append([]codextui.ModelPickerOption(nil), m.modelPickerOpts...)
	if len(options) == 0 {
		options = codextui.BundledModelPickerOptions()
	}
	picker := codextui.NewModelPicker(options, m.State.Model)
	if picker == nil || len(picker.Options) == 0 {
		m.notice = "No models are available right now."
		return m.fetchModelsForPicker()
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
	return m.fetchModelsForPicker()
}

// fetchModelsForPicker asynchronously fetches the current model catalog from the
// app server so the picker does not rely on a stale startup catalog (Rust
// #41467). It returns nil when no catalog callback is configured.
func (m *Model) fetchModelsForPicker() bubbletea.Cmd {
	if m == nil || m.onListModels == nil {
		return nil
	}
	m.nextModelsRequestID++
	requestID := m.nextModelsRequestID
	m.pendingModelsRequestID = requestID
	return func() bubbletea.Msg {
		options, err := m.onListModels(true)
		return ModelsResultMsg{RequestID: requestID, Options: options, Err: err}
	}
}

// applyModelsResult applies a fetched model catalog to the picker options and
// refreshes an open model picker in place (Rust #41467). Stale, failed, and
// empty responses are ignored.
func (m *Model) applyModelsResult(msg ModelsResultMsg) {
	if m == nil || msg.RequestID == 0 || msg.RequestID != m.pendingModelsRequestID {
		return
	}
	m.pendingModelsRequestID = 0
	if msg.Err != nil || len(msg.Options) == 0 {
		return
	}
	m.modelPickerOpts = append([]codextui.ModelPickerOption(nil), msg.Options...)
	m.refreshModelPicker()
}

// refreshModelPicker rebuilds an open model picker from the updated catalog,
// preserving the highlighted model.
func (m *Model) refreshModelPicker() {
	if m == nil || m.modal == nil || m.modal.modelPicker == nil {
		return
	}
	// Rust #41467: apply accepted catalog updates to the new-thread default. If
	// the current model is no longer available, fall back to the catalog default
	// (or the first available model).
	if m.State != nil && !m.modelIsAvailable(m.State.Model) {
		fallback := ""
		for _, option := range m.modelPickerOpts {
			if option.IsDefault {
				fallback = option.ID
				break
			}
		}
		if fallback == "" && len(m.modelPickerOpts) > 0 {
			fallback = m.modelPickerOpts[0].ID
		}
		if fallback != "" {
			m.State.Model = fallback
		}
	}
	picker := codextui.NewModelPicker(append([]codextui.ModelPickerOption(nil), m.modelPickerOpts...), m.State.Model)
	if picker == nil || len(picker.Options) == 0 {
		return
	}
	selected := m.modal.selected
	if selected >= len(picker.Options) {
		selected = len(picker.Options) - 1
	}
	m.modal.modelPicker = picker
	m.modal.selected = selected
	m.modal.modelPicker.Select(selected)
	modalOptions := make([]ModalOption, 0, len(picker.Options))
	for i, option := range picker.Options {
		modalOptions = append(modalOptions, ModalOption{
			ID:          option.ID,
			Label:       modelPickerLabel(option),
			Description: option.Description,
			Shortcut:    modelPickerShortcut(i),
		})
	}
	m.modal.options = modalOptions
}

// modelIsAvailable reports whether model is present in the refreshed catalog.
func (m *Model) modelIsAvailable(model string) bool {
	if strings.TrimSpace(model) == "" {
		return true
	}
	for _, option := range m.modelPickerOpts {
		if option.ID == model {
			return true
		}
	}
	return false
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
