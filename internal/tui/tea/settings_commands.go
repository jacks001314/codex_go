package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/internal/features"
	codextui "codex_go/internal/tui"
	chatwidget "codex_go/internal/tui/chatwidget"
)

const (
	settingsWriteKindPersonality         = "personality"
	settingsWriteKindExperimental        = "experimental"
	settingsWriteKindRateLimitModelNudge = "rate_limit_model_nudge"
	settingsWriteKindTheme               = "theme"
	settingsWriteKindPet                 = "pet"
)

func initialPersonality(state *codextui.State, configured chatwidget.Personality) chatwidget.Personality {
	if strings.TrimSpace(string(configured)) != "" {
		return normalizePersonalityTea(configured)
	}
	if state != nil && strings.TrimSpace(state.Personality) != "" {
		return normalizePersonalityTea(chatwidget.Personality(state.Personality))
	}
	return chatwidget.PersonalityFriendly
}

func normalizePersonalityTea(personality chatwidget.Personality) chatwidget.Personality {
	switch chatwidget.Personality(strings.ToLower(strings.TrimSpace(string(personality)))) {
	case chatwidget.PersonalityPragmatic:
		return chatwidget.PersonalityPragmatic
	case chatwidget.PersonalityNone:
		return chatwidget.PersonalityNone
	default:
		return chatwidget.PersonalityFriendly
	}
}

func (m *Model) openPersonalityMenu() {
	if m == nil {
		return
	}
	if !features.Enabled(m.featureSettings, "personality") {
		m.notice = "Personality selection is disabled by features.personality=false."
		m.refreshTranscript()
		return
	}
	model := ""
	if m.State != nil {
		model = m.State.Model
	}
	result := chatwidget.NewPersonalityPopup(m.personality, true, true, model)
	switch result.Kind {
	case chatwidget.SettingsPopupInfo, chatwidget.SettingsPopupError:
		m.notice = result.Message
		m.refreshTranscript()
		return
	}
	options := make([]ModalOption, 0, len(result.View.Items))
	for _, item := range result.View.Items {
		description := strings.TrimSpace(item.Description)
		if item.Current {
			if description != "" {
				description += " "
			}
			description += "(current)"
		}
		options = append(options, ModalOption{
			ID:          string(item.Personality),
			Label:       item.Name,
			Description: description,
			Disabled:    item.Disabled,
		})
	}
	m.openModal(ModalRequestMsg{
		ID:      "personality",
		Kind:    ModalKindPersonality,
		Title:   result.View.Title,
		Body:    result.View.Subtitle,
		Options: options,
	})
}

func (m *Model) applyPersonalityCommand(args string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if strings.TrimSpace(args) == "" {
		m.openPersonalityMenu()
		return nil
	}
	personality, ok := parsePersonalityArgument(args)
	if !ok {
		m.notice = "Usage: /personality [friendly|pragmatic|none]"
		m.refreshTranscript()
		return nil
	}
	return m.setPersonality(personality)
}

func parsePersonalityArgument(args string) (chatwidget.Personality, bool) {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(args)))
	if len(fields) == 0 {
		return "", false
	}
	switch fields[0] {
	case string(chatwidget.PersonalityFriendly):
		return chatwidget.PersonalityFriendly, true
	case string(chatwidget.PersonalityPragmatic):
		return chatwidget.PersonalityPragmatic, true
	case string(chatwidget.PersonalityNone):
		return chatwidget.PersonalityNone, true
	default:
		return "", false
	}
}

func (m *Model) applyPersonalityModalOption(optionID string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	return m.setPersonality(normalizePersonalityTea(chatwidget.Personality(optionID)))
}

func (m *Model) setPersonality(personality chatwidget.Personality) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	m.personality = personality
	if m.State != nil {
		m.State.Personality = string(personality)
	}
	m.notice = "Personality set to " + chatwidget.PersonalityLabel(personality)
	m.refreshTranscript()
	cmds := []bubbletea.Cmd{m.refreshStatusControlsCmd()}
	if m.onWriteSettings != nil {
		cmds = append(cmds, m.writeSettings(settingsWriteKindPersonality, []SettingsEdit{{
			KeyPath: "personality",
			Value:   string(personality),
		}}))
	}
	return bubbletea.Batch(cmds...)
}

func (m *Model) openExperimentalMenu() {
	if m == nil {
		return
	}
	view := chatwidget.NewExperimentalFeaturesView(m.featureSettings)
	m.experimentalItems = append([]chatwidget.ExperimentalFeatureOption(nil), view.Items...)
	if len(view.Items) == 0 {
		m.notice = "No experimental features available."
		m.refreshTranscript()
		return
	}
	m.openModal(ModalRequestMsg{
		ID:      "experimental",
		Kind:    ModalKindExperimental,
		Title:   view.Title,
		Body:    "Toggle experimental features. Changes are saved to config.toml.",
		Options: experimentalModalOptions(m.experimentalItems),
	})
}

func (m *Model) applyExperimentalCommand(args string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	fields := strings.Fields(args)
	if len(fields) == 0 {
		m.openExperimentalMenu()
		return nil
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
		Key:     key,
		Name:    key,
		Enabled: enabled,
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
		edits = append(edits, SettingsEdit{KeyPath: "features." + key, Value: item.Enabled})
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
		m.notice = "Failed to save settings: " + msg.Err.Error()
		m.refreshTranscript()
		return
	}
	if msg.Result.FeatureSettings != nil {
		m.featureSettings = cloneBoolMapTea(msg.Result.FeatureSettings)
	}
	if strings.TrimSpace(string(msg.Result.Personality)) != "" {
		m.personality = normalizePersonalityTea(msg.Result.Personality)
		if m.State != nil {
			m.State.Personality = string(m.personality)
		}
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
	if strings.TrimSpace(msg.Result.FilePath) != "" {
		switch msg.Kind {
		case settingsWriteKindPersonality:
			m.notice = "Personality set to " + chatwidget.PersonalityLabel(m.personality) + ". Saved to " + strings.TrimSpace(msg.Result.FilePath) + "."
		case settingsWriteKindExperimental:
			m.notice = "Experimental features saved to " + strings.TrimSpace(msg.Result.FilePath) + "."
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
		default:
			m.notice = "Settings saved to " + strings.TrimSpace(msg.Result.FilePath) + "."
		}
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
