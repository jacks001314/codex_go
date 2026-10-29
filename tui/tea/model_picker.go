package tea

import (
	"strings"

	"codex_go/config"
	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
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
		modalOptions = append(modalOptions, m.modelPickerModalOption(option, i))
	}
	m.openModal(ModalRequestMsg{
		ID:      "model-picker",
		Kind:    ModalKindPicker,
		Title:   "Select Model and Effort",
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
	cwd := m.modelListCWD()
	return func() bubbletea.Msg {
		options, err := m.onListModels(cwd, true)
		return ModelsResultMsg{RequestID: requestID, Options: options, Err: err}
	}
}

// modelListCWD is the working directory whose model catalog should be read
// (Rust #55682: an attached session's project replaces the launch directory).
func (m *Model) modelListCWD() string {
	if m == nil {
		return ""
	}
	if cwd := strings.TrimSpace(m.sessionCWD); cwd != "" {
		return cwd
	}
	if m.State != nil {
		return strings.TrimSpace(m.State.CWD)
	}
	return ""
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
	// Rust #51253 (app_server_session bootstrap): the catalog the server hands
	// back is constrained by the server's requirements before the picker and the
	// service-tier commands read it, so an older server's shared Fast gate (and
	// a server that allows the independent Ultra Fast policy) both hold here.
	m.modelPickerOpts = codextui.ConstrainServerServiceTierOptions(append([]codextui.ModelPickerOption(nil), msg.Options...), m.serviceTierRequirements)
	m.refreshModelPicker()
	m.refreshServiceTierCommands()
}

// refreshServiceTierCommands recomputes the slash-command service-tier entries
// for the current model from the model picker catalog (Rust #41467
// sync_service_tier_commands / set_model). It is called when the selected model
// changes or the catalog refreshes so the fast-mode / slash-command surface
// stays in sync with the active model's service tiers.
func (m *Model) refreshServiceTierCommands() {
	if m == nil || m.State == nil {
		return
	}
	options := m.modelPickerOpts
	if len(options) == 0 {
		options = codextui.BundledModelPickerOptions()
	}
	for _, option := range options {
		if option.ID == m.State.Model {
			commands := bottompane.ServiceTierCommandsFromIDs(option.ServiceTiers)
			commands = serviceTierCommandsForPolicy(m.featureSettings, commands)
			m.serviceTierCommands = constrainServiceTierCommandsByRequirements(m.serviceTierRequirements, commands)
			return
		}
	}
}

// constrainServiceTierCommandsByRequirements drops the service tiers the
// server's reported requirements do not allow (Rust #51253
// constrain_server_service_tiers). It is the command-list form of the catalog
// constraining: the startup `ServiceTierCommands` option is not derived from the
// picker catalog, so it is filtered with the same policy here.
func constrainServiceTierCommandsByRequirements(requirements *config.ConfigRequirementsReadResponse, commands []bottompane.ServiceTierCommand) []bottompane.ServiceTierCommand {
	tierEnabled, ok := codextui.ServiceTierPolicyFromRequirements(requirements)
	if !ok {
		return commands
	}
	filtered := make([]bottompane.ServiceTierCommand, 0, len(commands))
	for _, command := range commands {
		if tierEnabled(command.ID) {
			filtered = append(filtered, command)
		}
	}
	return filtered
}

// serviceTierCommandsForPolicy drops the service tiers the selected policies
// disable (Rust #51253 current_model_service_tier_commands: `.filter(|tier|
// self.config.features.service_tier_enabled(&tier.id))`). A tier follows the
// policy that owns it, so disabling fast_mode alone keeps Ultra Fast available
// and vice versa.
func serviceTierCommandsForPolicy(featureSettings map[string]bool, commands []bottompane.ServiceTierCommand) []bottompane.ServiceTierCommand {
	filtered := make([]bottompane.ServiceTierCommand, 0, len(commands))
	for _, command := range commands {
		if codextui.ServiceTierEnabled(featureSettings, command.ID) {
			filtered = append(filtered, command)
		}
	}
	return filtered
}

// serviceTierPolicyFeatureChanged mirrors Rust ChatWidget::set_feature_enabled
// (#51253, chatwidget/settings.rs): a Fast or Ultra Fast policy change refreshes
// the effective tier and the service-tier commands, while other features leave
// them alone.
func serviceTierPolicyFeatureChanged(before map[string]bool, after map[string]bool) bool {
	for _, key := range []string{codextui.FeatureKeyFastMode, codextui.FeatureKeyUltrafastMode} {
		beforeValue, beforeSet := before[key]
		afterValue, afterSet := after[key]
		if beforeSet != afterSet || beforeValue != afterValue {
			return true
		}
	}
	return false
}

// refreshModelPicker rebuilds an open model picker from the updated catalog,
// preserving the highlighted model.
func (m *Model) refreshModelPicker() {
	if m == nil || m.modal == nil {
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
	// Rust #41467: refresh an open reasoning submenu in place even when the model
	// picker modal has been replaced by the reasoning modal.
	if m.modal.modelReasoning != nil && m.modal.modelReasoning.Model.ID != "" {
		for _, option := range m.modelPickerOpts {
			if option.ID == m.modal.modelReasoning.Model.ID {
				if m.modal.advancedReasoning {
					option, _ = m.advancedReasoningModelOption(option)
				}
				if refreshed := codextui.NewModelReasoningPicker(option, m.State.EffectiveReasoningEffort()); refreshed != nil {
					m.modal.modelReasoning = refreshed
					m.modal.options = m.modelReasoningModalOptions(refreshed)
				}
				break
			}
		}
	}
	if m.modal.modelPicker == nil {
		return
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
		modalOptions = append(modalOptions, m.modelPickerModalOption(option, i))
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
		m.refreshServiceTierCommands()
		m.notice = strings.TrimSpace(m.State.RenderSetting("Model", m.State.Model))
		return
	}
	modalOptions := m.modelReasoningModalOptions(picker)
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
		m.modal.advancedReasoning = false
	}
}

func (m *Model) advancedReasoningModelOption(option codextui.ModelPickerOption) (codextui.ModelPickerOption, bool) {
	choices := option.AdvancedReasoningEfforts()
	if len(choices) == 0 {
		return option, false
	}
	option.SupportedReasoningEfforts = choices
	option.DefaultReasoningEffort = choices[0].Effort
	return option, true
}

// openAdvancedReasoningPicker mirrors Rust open_advanced_reasoning_popup: the
// parent model row opens a Max/Ultra-only submenu where every final choice has
// the session-only shortcut.
func (m *Model) openAdvancedReasoningPicker(option codextui.ModelPickerOption) {
	option, ok := m.advancedReasoningModelOption(option)
	if !ok {
		m.openModelReasoningPicker(option)
		return
	}
	picker := codextui.NewModelReasoningPicker(option, m.State.EffectiveReasoningEffort())
	if picker == nil || len(picker.Options) == 0 {
		return
	}
	m.openModal(ModalRequestMsg{
		ID:      "advanced-reasoning-picker",
		Kind:    ModalKindPicker,
		Title:   "Advanced Reasoning",
		Body:    "\u26a0 Consumes usage limits faster",
		Options: m.modelReasoningModalOptions(picker),
	})
	if m.modal != nil {
		m.modal.modelReasoning = picker
		m.modal.selected = picker.Selected
		m.modal.advancedReasoning = true
	}
}

func (m *Model) openPlanReasoningScopePicker(option codextui.ModelPickerOption, effort string) {
	picker := codextui.NewPlanReasoningScopePicker(option, effort, m.State.PlanModeReasoningEffort)
	if picker == nil || len(picker.Options) == 0 {
		m.State.Model = option.ID
		m.State.PlanModeReasoningEffort = strings.TrimSpace(effort)
		m.refreshServiceTierCommands()
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
	// Rust #51235: model rows carry "(current)" and nothing else; the catalog
	// default is still tracked by ModelPickerOption.IsDefault for preselection.
	if option.IsCurrent {
		label += " (current)"
	}
	return label
}

func modelPickerShortcut(index int) string {
	if index >= 0 && index < 9 {
		return string(rune('1' + index))
	}
	return ""
}

// ModelCatalogResultMsg carries the hidden-inclusive model catalog used to
// resolve backend-authorized model switches (the Reserve model is picker-hidden).
type ModelCatalogResultMsg struct {
	RequestID uint64
	Options   []codextui.ModelPickerOption
	Err       error
}

// fetchModelCatalog asynchronously fetches the hidden-inclusive catalog. It
// returns nil when no catalog callback is configured.
func (m *Model) fetchModelCatalog() bubbletea.Cmd {
	if m == nil || m.onListModels == nil {
		return nil
	}
	m.nextModelCatalogRequestID++
	requestID := m.nextModelCatalogRequestID
	m.pendingModelCatalogRequestID = requestID
	cwd := m.modelListCWD()
	return func() bubbletea.Msg {
		options, err := m.onListModels(cwd, true)
		return ModelCatalogResultMsg{RequestID: requestID, Options: options, Err: err}
	}
}

// applyModelCatalogResult stores the hidden-inclusive catalog. Stale, failed,
// and empty responses are ignored.
func (m *Model) applyModelCatalogResult(msg ModelCatalogResultMsg) {
	if m == nil || msg.RequestID == 0 || msg.RequestID != m.pendingModelCatalogRequestID {
		return
	}
	m.pendingModelCatalogRequestID = 0
	if msg.Err != nil || len(msg.Options) == 0 {
		return
	}
	// Rust #51253: the hidden-inclusive catalog is constrained the same way as
	// the picker catalog (the Reserve model is picker-hidden but still resolves
	// through this list).
	m.modelCatalogOpts = codextui.ConstrainServerServiceTierOptions(append([]codextui.ModelPickerOption(nil), msg.Options...), m.serviceTierRequirements)
}
