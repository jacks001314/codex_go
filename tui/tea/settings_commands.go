package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/features"
	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
	historycell "codex_go/tui/history_cell"
)

const (
	settingsWriteKindExperimental        = "experimental"
	settingsWriteKindRateLimitModelNudge = "rate_limit_model_nudge"
	settingsWriteKindTheme               = "theme"
	settingsWriteKindPet                 = "pet"
	settingsWriteKindServiceTier         = "service_tier"
)

// initialPersonality carries the configured personality into the model's
// state. Rust #44935 removed TUI personality selection, so there is no
// implicit default.
func initialPersonality(state *codextui.State, configured chatwidget.Personality) chatwidget.Personality {
	if value := strings.TrimSpace(string(configured)); value != "" {
		return chatwidget.Personality(value)
	}
	if state != nil {
		return chatwidget.Personality(strings.TrimSpace(state.Personality))
	}
	return ""
}

func (m *Model) applyFastServiceTier() bubbletea.Cmd {
	if m == nil || m.State == nil {
		return nil
	}
	fastTier := m.fastServiceTierCommand()
	if !features.Enabled(m.featureSettings, "fast_mode") || fastTier == nil || strings.TrimSpace(fastTier.ID) == "" {
		m.notice = "Fast mode is unavailable for the current model."
		m.refreshTranscript()
		return nil
	}
	next := strings.TrimSpace(fastTier.ID)
	if strings.TrimSpace(m.State.ServiceTier) == next {
		next = chatwidget.ServiceTierDefaultRequestValue
	}
	m.State.ServiceTier = next
	if m.onWriteSettings == nil {
		m.notice = "Service tier set to " + next
		m.refreshTranscript()
		return nil
	}
	configValue := next
	if next == strings.TrimSpace(fastTier.ID) {
		configValue = "fast"
	}
	return m.writeSettings(settingsWriteKindServiceTier, []SettingsEdit{{KeyPath: "service_tier", Value: configValue}})
}

// openExperimentalMenu opens the /experimental popup. Rust populates it from the
// app server's experimentalFeature/list catalog (starting with an empty list and
// a loading status); the compiled registry is only a fallback when no reader is
// wired.
func (m *Model) openExperimentalMenu() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	m.experimentalItems = nil
	m.experimentalFeaturesStatus = ""
	m.experimentalFeaturesGeneration++
	generation := m.experimentalFeaturesGeneration
	var cmd bubbletea.Cmd
	if m.onReadExperimentalFeatures != nil {
		m.experimentalFeaturesStatus = experimentalFeaturesLoadingStatus
		reader := m.onReadExperimentalFeatures
		threadID := m.currentThreadID()
		cmd = func() bubbletea.Msg {
			entries, err := reader(threadID)
			return ExperimentalFeaturesResultMsg{Generation: generation, Features: entries, Err: err}
		}
	} else {
		view := chatwidget.NewExperimentalFeaturesView(m.featureSettings)
		m.experimentalItems = append([]chatwidget.ExperimentalFeatureOption(nil), view.Items...)
		if len(view.Items) == 0 {
			m.notice = "No experimental features available."
			m.refreshTranscript()
			return nil
		}
	}
	m.openModal(ModalRequestMsg{
		ID:      "experimental",
		Kind:    ModalKindExperimental,
		Title:   "Experimental Features",
		Body:    experimentalModalBody(m.experimentalFeaturesStatus),
		Options: experimentalModalOptions(m.experimentalItems),
	})
	return cmd
}

func (m *Model) applyExperimentalCommand(args string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	fields := strings.Fields(args)
	if len(fields) == 0 {
		return m.openExperimentalMenu()
	}
	key := strings.TrimSpace(fields[0])
	if !experimentalFeatureVisible(key) {
		m.notice = "Unknown experimental feature: " + key
		m.refreshTranscript()
		return nil
	}
	enabled := !features.Enabled(m.featureSettings, key)
	if len(fields) > 1 {
		parsed, ok := parseExperimentalToggle(fields[1], enabled)
		if !ok {
			m.notice = "Usage: /experimental [FEATURE on|off|toggle]"
			m.refreshTranscript()
			return nil
		}
		enabled = parsed
	}
	return m.setExperimentalFeatures([]chatwidget.ExperimentalFeatureOption{{
		Key:            key,
		Name:           key,
		Enabled:        enabled,
		DefaultEnabled: features.Defaults()[key],
	}})
}

func (m *Model) applyExperimentalModalOption(optionID string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if len(m.experimentalItems) == 0 {
		m.notice = "Experimental Features"
		m.refreshTranscript()
		return nil
	}
	return m.setExperimentalFeatures(m.experimentalItems)
}

func parseExperimentalToggle(value string, toggleValue bool) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "on", "true", "enable", "enabled", "yes":
		return true, true
	case "off", "false", "disable", "disabled", "no":
		return false, true
	case "toggle":
		return toggleValue, true
	default:
		return false, false
	}
}

func (m *Model) setExperimentalFeatures(items []chatwidget.ExperimentalFeatureOption) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if m.featureSettings == nil {
		m.featureSettings = map[string]bool{}
	}
	edits := make([]SettingsEdit, 0, len(items))
	changed := 0
	for _, item := range items {
		key := strings.TrimSpace(item.Key)
		if key == "" {
			continue
		}
		if features.Enabled(m.featureSettings, key) != item.Enabled {
			changed++
		}
		m.featureSettings[key] = item.Enabled
		// Rust experimental_features::write: enabling writes true, disabling a
		// default-enabled feature clears the override (null) instead of writing
		// false, and disabling a default-off feature writes false.
		value := any(item.Enabled)
		if !item.Enabled && item.DefaultEnabled {
			value = nil
		}
		edits = append(edits, SettingsEdit{KeyPath: "features." + key, Value: value})
	}
	switch {
	case len(items) == 1:
		item := items[0]
		if item.Enabled {
			m.notice = "Feature " + item.Key + " enabled."
		} else {
			m.notice = "Feature " + item.Key + " disabled."
		}
	case changed == 0:
		m.notice = "Experimental features unchanged."
	default:
		m.notice = "Experimental features saved."
	}
	m.refreshTranscript()
	if m.onWriteSettings != nil && len(edits) > 0 {
		requested := make(map[string]bool, len(items))
		for _, item := range items {
			if key := strings.TrimSpace(item.Key); key != "" {
				requested[key] = item.Enabled
			}
		}
		m.pendingExperimentalFeatureUpdates = requested
		return m.writeSettings(settingsWriteKindExperimental, edits)
	}
	return nil
}

func experimentalModalOptions(items []chatwidget.ExperimentalFeatureOption) []ModalOption {
	options := make([]ModalOption, 0, len(items))
	for _, item := range items {
		options = append(options, ModalOption{
			ID:          item.Key,
			Label:       experimentalFeatureLabel(item),
			Description: item.Description,
		})
	}
	return options
}

func experimentalFeatureLabel(item chatwidget.ExperimentalFeatureOption) string {
	if item.Enabled {
		return "[x] " + item.Name
	}
	return "[ ] " + item.Name
}

func (m *Model) toggleExperimentalSelection() {
	if m == nil || m.modal == nil || m.modal.kind != ModalKindExperimental {
		return
	}
	index := m.modal.selected
	if index < 0 || index >= len(m.experimentalItems) || index >= len(m.modal.options) {
		return
	}
	m.experimentalItems[index].Enabled = !m.experimentalItems[index].Enabled
	m.modal.options[index].Label = experimentalFeatureLabel(m.experimentalItems[index])
}

func experimentalFeatureVisible(key string) bool {
	key = strings.TrimSpace(key)
	for _, spec := range features.Registry {
		if spec.Key == key &&
			spec.Stage == features.StageExperimental &&
			strings.TrimSpace(spec.ExperimentalName) != "" &&
			strings.TrimSpace(spec.ExperimentalMenuDescription) != "" {
			return true
		}
	}
	return false
}

func (m *Model) writeSettings(kind string, edits []SettingsEdit) bubbletea.Cmd {
	if m == nil || m.onWriteSettings == nil || len(edits) == 0 {
		return nil
	}
	copied := make([]SettingsEdit, 0, len(edits))
	for _, edit := range edits {
		if strings.TrimSpace(edit.KeyPath) == "" {
			continue
		}
		copied = append(copied, SettingsEdit{
			KeyPath: strings.TrimSpace(edit.KeyPath),
			Value:   edit.Value,
		})
	}
	if len(copied) == 0 {
		return nil
	}
	requestID := m.nextSettingsRequest()
	m.pendingSettingsRequestID = requestID
	return func() bubbletea.Msg {
		result, err := m.onWriteSettings(copied)
		return SettingsWriteResultMsg{RequestID: requestID, Kind: kind, Result: result, Err: err}
	}
}

func (m *Model) nextSettingsRequest() uint64 {
	m.nextSettingsRequestID++
	if m.nextSettingsRequestID == 0 {
		m.nextSettingsRequestID = 1
	}
	return m.nextSettingsRequestID
}

func (m *Model) applySettingsWriteResult(msg SettingsWriteResultMsg) {
	if m == nil || m.pendingSettingsRequestID != msg.RequestID {
		return
	}
	m.pendingSettingsRequestID = 0
	if msg.Err != nil {
		if msg.Kind == settingsWriteKindServiceTier {
			m.notice = "Failed to save default service tier: " + msg.Err.Error()
		} else if msg.Kind == settingsWriteKindMemories {
			if strings.HasPrefix(msg.Err.Error(), "Saved memory settings,") {
				m.notice = msg.Err.Error()
			} else {
				m.notice = "Failed to save memory settings: " + msg.Err.Error()
			}
		} else {
			m.notice = "Failed to save settings: " + msg.Err.Error()
		}
		m.refreshTranscript()
		return
	}
	if msg.Result.FeatureSettings != nil {
		m.featureSettings = cloneBoolMapTea(msg.Result.FeatureSettings)
	}
	// Rust experimental_features::write readback: a saved value that differs from
	// the selection (or a higher-priority setting winning) warns instead of
	// reporting a plain save.
	experimentalOverridden := false
	if msg.Kind == settingsWriteKindExperimental && len(m.pendingExperimentalFeatureUpdates) > 0 {
		for key, requested := range m.pendingExperimentalFeatureUpdates {
			if features.Enabled(m.featureSettings, key) != requested {
				experimentalOverridden = true
				break
			}
		}
	}
	m.pendingExperimentalFeatureUpdates = nil
	if msg.Result.UseMemories != nil {
		m.useMemories = *msg.Result.UseMemories
	}
	if msg.Result.GenerateMemories != nil {
		m.generateMemories = *msg.Result.GenerateMemories
	}
	if msg.Result.FeedbackEnabled != nil {
		m.feedbackEnabled = *msg.Result.FeedbackEnabled
	}
	if msg.Result.AnimationsEnabled != nil {
		m.animationsEnabled = *msg.Result.AnimationsEnabled
	}
	if msg.Result.StatusLineUseColors != nil {
		m.statusLineUseColors = *msg.Result.StatusLineUseColors
		if m.statusControls != nil {
			m.statusControls.StatusLineUseThemeColors = m.statusLineUseColors
		}
	}
	if msg.Result.QuestionEscBack != nil {
		m.questionEscBack = *msg.Result.QuestionEscBack
	}
	if msg.Result.AutoRecap != nil {
		m.disableAutoRecap = !*msg.Result.AutoRecap
	}
	if msg.Result.Notifications != nil {
		m.notificationSettings = notificationSettingsOrDefault(msg.Result.Notifications)
	}
	if msg.Result.NotificationMethod != "" {
		m.notificationMethod = notificationMethodOrDefault(msg.Result.NotificationMethod)
	}
	if msg.Result.NotificationCondition != "" {
		m.notificationCondition = notificationConditionOrDefault(msg.Result.NotificationCondition)
	}
	if msg.Result.PermissionRequirements != nil {
		m.permissionRequirements = clonePermissionRequirementsTea(msg.Result.PermissionRequirements)
	}
	if msg.Result.HideRateLimitModelNudge != nil {
		m.hideRateLimitModelNudge = *msg.Result.HideRateLimitModelNudge
		if m.hideRateLimitModelNudge {
			m.rateLimitSwitchPrompt = chatwidget.RateLimitSwitchPromptIdle
		}
	}
	if strings.TrimSpace(msg.Result.TUITheme) != "" {
		m.tuiTheme = strings.TrimSpace(msg.Result.TUITheme)
	}
	if strings.TrimSpace(msg.Result.TUIPet) != "" {
		m.tuiPet = normalizePetIDTea(msg.Result.TUIPet)
	}
	if strings.TrimSpace(msg.Result.SessionPickerView) != "" {
		m.sessionPickerDensity = normalizeSessionPickerDensityTea(msg.Result.SessionPickerView)
	}
	if msg.Kind == settingsWriteKindMemoriesEnable {
		m.notice = ""
		m.applyHistoryCell(historycell.NewWarningEvent("Memories will be enabled in the next session."))
		return
	}
	if msg.Kind == settingsWriteKindMemories && strings.TrimSpace(msg.Result.FilePath) == "" {
		m.notice = "Memory settings updated."
	}
	if strings.TrimSpace(msg.Result.FilePath) != "" {
		switch msg.Kind {
		case settingsWriteKindExperimental:
			m.notice = "Experimental features saved to " + strings.TrimSpace(msg.Result.FilePath) + "."
		case settingsWriteKindMemories:
			m.notice = "Memory settings saved to " + strings.TrimSpace(msg.Result.FilePath) + "."
		case settingsWriteKindRateLimitModelNudge:
			m.notice = "Rate limit model switch reminders hidden. Saved to " + strings.TrimSpace(msg.Result.FilePath) + "."
		case settingsWriteKindTheme:
			m.notice = "Theme set to " + themeLabelTea(m.tuiTheme) + ". Saved to " + strings.TrimSpace(msg.Result.FilePath) + "."
		case settingsWriteKindPet:
			if m.tuiPet == chatwidget.DisabledPetID {
				m.notice = "Terminal pets disabled. Saved to " + strings.TrimSpace(msg.Result.FilePath) + "."
			} else {
				m.notice = "Pet set to " + petLabelTea(m.tuiPet) + ". Saved to " + strings.TrimSpace(msg.Result.FilePath) + "."
			}
		case settingsWriteKindServiceTier:
			m.notice = "Service tier set to " + strings.TrimSpace(m.State.ServiceTier)
		default:
			m.notice = "Settings saved to " + strings.TrimSpace(msg.Result.FilePath) + "."
		}
	}
	if experimentalOverridden {
		m.notice = "Changes were saved, but the configured values differ from your selections. A higher-priority setting may override them."
	}
	m.refreshTranscript()
}

func cloneBoolMapTea(values map[string]bool) map[string]bool {
	if values == nil {
		return nil
	}
	out := make(map[string]bool, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
