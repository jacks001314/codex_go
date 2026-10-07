package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/features"
	codextui "codex_go/tui"
	agentsoverview "codex_go/tui/agents_overview"
	chatwidget "codex_go/tui/chatwidget"
	historycell "codex_go/tui/history_cell"
	"codex_go/tui/markdown"
)

const (
	settingsWriteKindExperimental           = "experimental"
	settingsWriteKindRateLimitModelNudge    = "rate_limit_model_nudge"
	settingsWriteKindTheme                  = "theme"
	settingsWriteKindPet                    = "pet"
	settingsWriteKindServiceTier            = "service_tier"
	settingsWriteKindApprovalsReviewer      = "approvals_reviewer"
	settingsWriteKindAgentsOverviewGrouping = "agents_overview_grouping"
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
	// Rust #51253 (can_toggle_fast_mode_from_keybinding): the fast toggle needs
	// the Fast policy enabled and a fast tier the policy left in the catalog.
	if fastTier == nil || strings.TrimSpace(fastTier.ID) == "" || !codextui.ServiceTierEnabled(m.featureSettings, fastTier.ID) {
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
		m.setExperimentalItems(append([]chatwidget.ExperimentalFeatureOption(nil), view.Items...))
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
	serviceTierPolicyChanged := false
	for _, item := range items {
		key := strings.TrimSpace(item.Key)
		if key == "" {
			continue
		}
		if features.Enabled(m.featureSettings, key) != item.Enabled {
			changed++
		}
		previous := m.featureSettings[key]
		m.featureSettings[key] = item.Enabled
		if (key == codextui.FeatureKeyFastMode || key == codextui.FeatureKeyUltrafastMode) && previous != item.Enabled {
			serviceTierPolicyChanged = true
		}
		edits = append(edits, experimentalFeatureEdit(item))
	}
	if serviceTierPolicyChanged {
		// Rust #51253 (chatwidget/settings.rs set_feature_enabled): changing the
		// Fast or Ultra Fast policy re-syncs the service-tier commands.
		m.refreshServiceTierCommands()
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
	label := "[ ] "
	if item.Enabled {
		label = "[x] "
	}
	label += item.Name
	if !item.Writable {
		label += " (read-only)"
	}
	return label
}

func (m *Model) toggleExperimentalSelection() {
	if m == nil || m.modal == nil || m.modal.kind != ModalKindExperimental {
		return
	}
	index := m.modal.selected
	if index < 0 || index >= len(m.experimentalItems) || index >= len(m.modal.options) {
		return
	}
	// Rust toggle_selected: read-only rows and an in-flight save refuse toggles.
	if !m.experimentalItems[index].Writable || m.experimentalFeaturesSaving {
		return
	}
	m.experimentalItems[index].Enabled = !m.experimentalItems[index].Enabled
	m.modal.options[index].Label = experimentalFeatureLabel(m.experimentalItems[index])
}

// updateExperimentalModal handles the /experimental popup's keys. Space toggles
// the selected writable feature, Enter saves and keeps the popup open while the
// write is in flight, and a second Enter or Esc closes it while the write
// finishes (Rust ExperimentalFeaturesView::handle_key_event / on_ctrl_c).
func (m *Model) updateExperimentalModal(message bubbletea.KeyMsg) bubbletea.Cmd {
	if m == nil || m.modal == nil || m.modal.kind != ModalKindExperimental {
		return nil
	}
	switch message.Type {
	case bubbletea.KeyEsc:
		// Rust on_ctrl_c: retry the unconfirmed rows (or save pending changes),
		// then close; the write continues independently.
		cmd := m.saveExperimentalFeatures()
		m.modal = nil
		m.refreshTranscript()
		return cmd
	case bubbletea.KeySpace:
		m.toggleExperimentalSelection()
		return nil
	case bubbletea.KeyEnter:
		if m.experimentalFeaturesSaving || !m.experimentalFeaturesHaveUpdates() {
			m.modal = nil
			m.refreshTranscript()
			return nil
		}
		return m.saveExperimentalFeatures()
	case bubbletea.KeyUp:
		m.moveModalSelection(-1)
		m.disarmModalOption()
		return nil
	case bubbletea.KeyDown, bubbletea.KeyTab:
		m.moveModalSelection(1)
		m.disarmModalOption()
		return nil
	case bubbletea.KeyRunes:
		switch string(message.Runes) {
		case " ":
			m.toggleExperimentalSelection()
		case "k":
			m.moveModalSelection(-1)
			m.disarmModalOption()
		case "j":
			m.moveModalSelection(1)
			m.disarmModalOption()
		}
	}
	return nil
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
		// Rust #51510: a failed configuration reload preserves the live TUI
		// settings, so msg.Result is never applied on this path; only the
		// failure notice below is reported.
		if msg.Kind == settingsWriteKindExperimental {
			// Rust keeps the popup open with the failure as its status and retains
			// the unconfirmed selections for an explicit retry.
			m.experimentalFeaturesSaving = false
			m.experimentalFeaturesStatus = msg.Err.Error()
			m.refreshExperimentalModal()
			m.notice = "Failed to save settings: " + msg.Err.Error()
			m.refreshTranscript()
			return
		}
		if msg.Kind == settingsWriteKindAgentsOverviewGrouping {
			// Rust #50786: report the save failure inside Command Center while the
			// selected grouping stays active.
			m.agentsOverviewNotice = "Failed to save Command Center grouping: " + msg.Err.Error()
			m.refreshTranscript()
			return
		}
		if msg.Kind == settingsWriteKindApprovalsReviewer {
			// Rust #46036: report the persistence failure as an error message,
			// keeping the backend's actionable cause (config file location and
			// parse error) instead of a bare save failure.
			m.notice = ""
			m.applyHistoryCell(historycell.NewErrorEvent("Failed to save approvals reviewer: " + msg.Err.Error()))
			m.refreshTranscript()
			return
		}
		if msg.Kind == settingsWriteKindServiceTier {
			// Rust #49835: report the underlying configuration error and explain
			// how to retry saving the default without interrupting the task.
			m.notice = "Failed to save default service tier: " + msg.Err.Error() +
				"\nYou can continue this task. To save the default, resolve the error above, then switch to a different tier and back to the desired tier."
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
	// Rust #51510: only a reload that actually succeeded may stage its result
	// onto the live TUI settings; a failed reload must keep the live settings.
	if !ShouldStageReloadedLocalSettings(&msg.Result, msg.Err) {
		m.refreshTranscript()
		return
	}
	// Rust #51510: a successful reload stages its preferences on the local
	// settings record and then applies the record to the live TUI state (Rust
	// LocalSettings::reloaded + App::local_settings). A failed reload never
	// reaches this point, so the staged record keeps the live settings.
	m.applyLocalSettings(m.localSettings.Reloaded(msg.Result))
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
		case settingsWriteKindAgentsOverviewGrouping:
			// Rust #50786 saves the grouping silently; Command Center reports only
			// its own notice.
		default:
			m.notice = "Settings saved to " + strings.TrimSpace(msg.Result.FilePath) + "."
		}
	}
	if experimentalOverridden {
		m.notice = "Changes were saved, but the configured values differ from your selections. A higher-priority setting may override them."
	}
	if msg.Kind == settingsWriteKindExperimental {
		m.applyExperimentalWriteReadback(experimentalOverridden)
	}
	m.refreshTranscript()
}

// applyLocalSettings installs a staged local-settings record on the live TUI
// state (Rust #51510): the record's preferences are merged into the model with
// the usual "nil keeps the live value" rule, and the launcher-owned fields are
// restored from the record — never from the reloaded source — so a configuration
// reload cannot change this launch's terminal ownership (`Model.noAltScreen`,
// the Go half of Rust's `transcript_mode` / `tui.alternate_screen` pair).
func (m *Model) applyLocalSettings(settings LocalSettings) {
	if m == nil {
		return
	}
	m.localSettings = settings
	m.applyLocalSettingsValues(settings.Tui)
	m.noAltScreen = settings.AlternateScreen
}

// applyLocalSettingsValues applies one client-owned preference set to the live
// TUI state (Rust #51510 / the field half of LocalSettings::reloaded). Every
// nil pointer field keeps the live value, which is why a partial record — or an
// empty one — can never clear a live preference.
func (m *Model) applyLocalSettingsValues(result SettingsWriteResult) {
	if m == nil {
		return
	}
	if result.FeatureSettings != nil {
		previousFeatures := m.featureSettings
		m.featureSettings = cloneBoolMapTea(result.FeatureSettings)
		if serviceTierPolicyFeatureChanged(previousFeatures, m.featureSettings) {
			// Rust #51253: a Fast or Ultra Fast policy change re-syncs the
			// service-tier commands.
			m.refreshServiceTierCommands()
		}
	}
	if result.UseMemories != nil {
		m.useMemories = *result.UseMemories
	}
	if result.GenerateMemories != nil {
		m.generateMemories = *result.GenerateMemories
	}
	if result.FeedbackEnabled != nil {
		m.feedbackEnabled = *result.FeedbackEnabled
	}
	if result.AnimationsEnabled != nil {
		m.animationsEnabled = *result.AnimationsEnabled
	}
	if result.MouseScrollSpeed != nil {
		// Rust #50209: the reloaded `tui.mouse_scroll_speed` reaches the live
		// transcript surfaces (main viewport and fullscreen pager).
		m.setMouseScrollSpeed(*result.MouseScrollSpeed)
	}
	if result.StatusLineUseColors != nil {
		m.statusLineUseColors = *result.StatusLineUseColors
		if m.statusControls != nil {
			m.statusControls.StatusLineUseThemeColors = m.statusLineUseColors
		}
	}
	if result.QuestionEscBack != nil {
		m.questionEscBack = *result.QuestionEscBack
	}
	if result.AutoRecap != nil {
		m.disableAutoRecap = !*result.AutoRecap
	}
	if result.ShowTooltips != nil {
		m.showTooltips = *result.ShowTooltips
	}
	if result.RightClickPaste != nil {
		m.setRightClickPasteMode(*result.RightClickPaste)
	}
	if result.Rendering != nil {
		// Rust refreshes the Markdown rendering preferences when the resolved
		// session settings become active (markdown_render::preferences::init).
		markdown.InitRendering(markdown.Rendering{
			Mermaid: result.Rendering.Mermaid,
			Math:    result.Rendering.Math,
			Tables:  result.Rendering.Tables,
			Lists:   result.Rendering.Lists,
		})
	}
	if result.Effects != nil {
		m.effects = *result.Effects
	}
	if result.Notifications != nil {
		m.notificationSettings = notificationSettingsOrDefault(result.Notifications)
	}
	if result.NotificationMethod != "" {
		m.notificationMethod = notificationMethodOrDefault(result.NotificationMethod)
	}
	if result.NotificationCondition != "" {
		m.notificationCondition = notificationConditionOrDefault(result.NotificationCondition)
	}
	if result.PermissionRequirements != nil {
		m.permissionRequirements = clonePermissionRequirementsTea(result.PermissionRequirements)
	}
	if result.HideRateLimitModelNudge != nil {
		m.hideRateLimitModelNudge = *result.HideRateLimitModelNudge
		if m.hideRateLimitModelNudge {
			m.rateLimitSwitchPrompt = chatwidget.RateLimitSwitchPromptIdle
		}
	}
	if strings.TrimSpace(result.TUITheme) != "" {
		m.tuiTheme = strings.TrimSpace(result.TUITheme)
	}
	if strings.TrimSpace(result.TUIPet) != "" {
		m.tuiPet = normalizePetIDTea(result.TUIPet)
	}
	if strings.TrimSpace(result.SessionPickerView) != "" {
		m.sessionPickerDensity = normalizeSessionPickerDensityTea(result.SessionPickerView)
	}
	if strings.TrimSpace(result.AgentsOverviewGrouping) != "" {
		// Rust #50786: a reloaded `tui.agents_overview_grouping` restores the
		// remembered Command Center grouping, live dashboard included.
		m.agentsOverviewGrouping = agentsoverview.ParseGroupingConfig(result.AgentsOverviewGrouping)
		if m.agentsOverview != nil {
			m.agentsOverview.State.Grouping = m.agentsOverviewGrouping
		}
	}
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
