package chatwidget

import (
	"sort"
	"strings"
)

const (
	ModelSelectionViewID          = "model-selection"
	AllModelsSelectionViewID      = "all-models-selection"
	ReasoningSelectionViewID      = "reasoning-selection"
	PlanReasoningScopeViewID      = "plan-reasoning-scope"
	DefaultOpenAIBaseURLForModels = "https://api.openai.com/v1"
)

const (
	ModelMenuActionSelectModel       UsageMenuAction = "model_select"
	ModelMenuActionOpenAllModels     UsageMenuAction = "model_open_all"
	ModelMenuActionOpenReasoning     UsageMenuAction = "model_open_reasoning"
	ModelMenuActionPlanScopePlanOnly UsageMenuAction = "model_plan_scope_plan_only"
	ModelMenuActionPlanScopeAllModes UsageMenuAction = "model_plan_scope_all_modes"
)

type ReasoningEffortPopupOption struct {
	Effort      string
	Description string
}

type ModelPopupPreset struct {
	Model                     string
	Description               string
	ShowInPicker              bool
	IsDefault                 bool
	DefaultReasoningEffort    string
	SupportedReasoningEfforts []ReasoningEffortPopupOption
}

type ModelPopupConfig struct {
	CurrentModel               string
	CurrentLabel               string
	CurrentReasoningEffort     string
	CurrentPlanReasoningEffort string
	PlanMode                   bool
	CollaborationModesEnabled  bool
	CustomOpenAIBaseURL        string
}

type ModelPopupResult struct {
	View                   SelectionView
	InfoMessage            string
	ApplyModel             string
	ApplyReasoningEffort   string
	OpenPlanReasoningScope bool
}

func NewModelPopupView(config ModelPopupConfig, presets []ModelPopupPreset) ModelPopupResult {
	presets = visibleModelPopupPresets(presets)
	if len(presets) == 0 {
		return ModelPopupResult{InfoMessage: "No models are available right now."}
	}

	auto, other := partitionAutoModelPresets(presets)
	if len(auto) == 0 {
		return NewAllModelsPopupView(config, other)
	}
	sort.SliceStable(auto, func(i, j int) bool {
		return autoModelOrder(auto[i].Model) < autoModelOrder(auto[j].Model)
	})

	currentModel := strings.TrimSpace(config.CurrentModel)
	currentLabel := currentModelLabel(config, presets)
	items := make([]SelectionItem, 0, len(auto)+1)
	for _, preset := range auto {
		modelID := strings.TrimSpace(preset.Model)
		items = append(items, SelectionItem{
			ID:              modelID,
			Name:            modelID,
			Description:     strings.TrimSpace(preset.Description),
			Action:          ModelMenuActionSelectModel,
			IsCurrent:       modelID == currentModel,
			IsDefault:       preset.IsDefault,
			DismissOnSelect: true,
		})
	}
	if len(other) > 0 {
		items = append(items, SelectionItem{
			ID:                         "all-models",
			Name:                       "All models",
			Description:                "Choose a specific model and reasoning level (current: " + currentLabel + ")",
			Action:                     ModelMenuActionOpenAllModels,
			IsCurrent:                  !selectionItemsHaveCurrent(items),
			DismissOnSelect:            true,
			DismissParentOnChildAccept: true,
		})
	}

	return ModelPopupResult{View: SelectionView{
		ViewID:      ModelSelectionViewID,
		Title:       "Select Model",
		Subtitle:    "Pick a quick auto mode or browse all models.",
		HeaderLines: modelPopupWarningLines(config),
		FooterHint:  standardPopupHintLine,
		AllowCancel: true,
		Items:       items,
	}}
}

func NewAllModelsPopupView(config ModelPopupConfig, presets []ModelPopupPreset) ModelPopupResult {
	presets = visibleModelPopupPresets(presets)
	if len(presets) == 0 {
		return ModelPopupResult{InfoMessage: "No additional models are available right now."}
	}
	currentModel := strings.TrimSpace(config.CurrentModel)
	items := make([]SelectionItem, 0, len(presets))
	for _, preset := range presets {
		modelID := strings.TrimSpace(preset.Model)
		singleEffort := len(effectiveReasoningPopupOptions(preset)) <= 1
		items = append(items, SelectionItem{
			ID:                         modelID,
			Name:                       modelID,
			Description:                strings.TrimSpace(preset.Description),
			Action:                     ModelMenuActionOpenReasoning,
			IsCurrent:                  modelID == currentModel,
			IsDefault:                  preset.IsDefault,
			DismissOnSelect:            singleEffort,
			DismissParentOnChildAccept: !singleEffort,
		})
	}
	return ModelPopupResult{View: SelectionView{
		ViewID:      AllModelsSelectionViewID,
		Title:       "Select Model and Effort",
		Subtitle:    "Access legacy models by running codex -m <model_name> or in your config.toml",
		HeaderLines: modelPopupWarningLines(config),
		FooterHint:  standardPopupHintLine,
		AllowCancel: true,
		Searchable:  true,
		Items:       items,
	}}
}

func NewReasoningPopupView(config ModelPopupConfig, preset ModelPopupPreset) ModelPopupResult {
	modelID := strings.TrimSpace(preset.Model)
	if modelID == "" {
		return ModelPopupResult{InfoMessage: "No model selected."}
	}
	options := effectiveReasoningPopupOptions(preset)
	if len(options) == 0 {
		return ModelPopupResult{InfoMessage: "No reasoning levels are available for " + modelID + "."}
	}
	defaultEffort := strings.TrimSpace(preset.DefaultReasoningEffort)
	if defaultEffort == "" || !reasoningOptionsContain(options, defaultEffort) {
		defaultEffort = options[0].Effort
	}
	if len(options) == 1 {
		selectedEffort := options[0].Effort
		if ShouldPromptPlanReasoningScope(config, modelID, selectedEffort) {
			return ModelPopupResult{ApplyModel: modelID, ApplyReasoningEffort: selectedEffort, OpenPlanReasoningScope: true}
		}
		return ModelPopupResult{ApplyModel: modelID, ApplyReasoningEffort: selectedEffort}
	}
	isCurrentModel := strings.TrimSpace(config.CurrentModel) == modelID
	currentEffort := defaultEffort
	if isCurrentModel {
		currentEffort = strings.TrimSpace(config.CurrentReasoningEffort)
		if config.PlanMode && strings.TrimSpace(config.CurrentPlanReasoningEffort) != "" {
			currentEffort = strings.TrimSpace(config.CurrentPlanReasoningEffort)
		}
		if currentEffort == "" {
			currentEffort = defaultEffort
		}
	}
	items := make([]SelectionItem, 0, len(options))
	initial := 0
	for i, option := range options {
		effort := strings.TrimSpace(option.Effort)
		label := ReasoningEffortPopupLabel(effort)
		if effort == defaultEffort {
			label += " (default)"
		}
		isCurrent := effort == currentEffort && currentEffort != ""
		if isCurrent {
			initial = i
		}
		items = append(items, SelectionItem{
			ID:                  effort,
			Name:                label,
			Description:         strings.TrimSpace(option.Description),
			SelectedDescription: reasoningSelectedDescription(modelID, effort, option.Description),
			Action:              ModelMenuActionSelectModel,
			IsCurrent:           isCurrent,
			IsDefault:           effort == defaultEffort,
			DismissOnSelect:     true,
		})
	}
	return ModelPopupResult{View: SelectionView{
		ViewID:               ReasoningSelectionViewID,
		Title:                "Select Reasoning Level for " + modelID,
		FooterHint:           standardPopupHintLine,
		AllowCancel:          true,
		InitialSelectedIndex: initial,
		Items:                items,
	}}
}

func NewPlanReasoningScopePopupView(selectedEffort string, currentPlanEffort string) SelectionView {
	reasoningPhrase := reasoningEffortSentenceLabelPopup(selectedEffort)
	source := "built-in Plan default"
	if strings.TrimSpace(currentPlanEffort) != "" {
		source = "user-chosen Plan override (" + reasoningEffortSentenceLabelPopup(currentPlanEffort) + ")"
	}
	return SelectionView{
		ViewID:      PlanReasoningScopeViewID,
		Title:       "Apply reasoning change",
		Subtitle:    "Choose where to apply " + reasoningPhrase + " reasoning.",
		FooterHint:  standardPopupHintLine,
		AllowCancel: true,
		Items: []SelectionItem{
			{
				ID:              "plan-only",
				Name:            "Apply to Plan mode override",
				Description:     "Always use " + reasoningPhrase + " reasoning in Plan mode.",
				Action:          ModelMenuActionPlanScopePlanOnly,
				DismissOnSelect: true,
			},
			{
				ID:              "all-modes",
				Name:            "Apply to global default and Plan mode override",
				Description:     "Set the global default reasoning level and the Plan mode override. This replaces the current " + source + ".",
				Action:          ModelMenuActionPlanScopeAllModes,
				DismissOnSelect: true,
			},
		},
	}
}

func ReasoningEffortPopupLabel(effort string) string {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "none":
		return "None"
	case "minimal":
		return "Minimal"
	case "low":
		return "Low"
	case "medium":
		return "Medium"
	case "high":
		return "High"
	case "xhigh", "x-high", "extra-high", "extra_high":
		return "Extra high"
	case "ultra":
		return "Ultra"
	case "":
		return "Default"
	default:
		return strings.TrimSpace(effort)
	}
}

func ShouldPromptPlanReasoningScope(config ModelPopupConfig, selectedModel string, selectedEffort string) bool {
	return config.CollaborationModesEnabled &&
		config.PlanMode &&
		strings.TrimSpace(selectedModel) == strings.TrimSpace(config.CurrentModel) &&
		(strings.TrimSpace(selectedEffort) != strings.TrimSpace(config.CurrentPlanReasoningEffort) ||
			strings.TrimSpace(selectedEffort) != strings.TrimSpace(config.CurrentReasoningEffort))
}

func visibleModelPopupPresets(presets []ModelPopupPreset) []ModelPopupPreset {
	out := make([]ModelPopupPreset, 0, len(presets))
	seen := map[string]bool{}
	for _, preset := range presets {
		modelID := strings.TrimSpace(preset.Model)
		if modelID == "" || seen[modelID] || !preset.ShowInPicker {
			continue
		}
		seen[modelID] = true
		preset.Model = modelID
		out = append(out, preset)
	}
	return out
}

func partitionAutoModelPresets(presets []ModelPopupPreset) ([]ModelPopupPreset, []ModelPopupPreset) {
	auto := []ModelPopupPreset{}
	other := []ModelPopupPreset{}
	for _, preset := range presets {
		if strings.HasPrefix(preset.Model, "codex-auto-") {
			auto = append(auto, preset)
		} else {
			other = append(other, preset)
		}
	}
	return auto, other
}

func autoModelOrder(model string) int {
	switch strings.TrimSpace(model) {
	case "codex-auto-fast":
		return 0
	case "codex-auto-balanced":
		return 1
	case "codex-auto-thorough":
		return 2
	default:
		return 3
	}
}

func currentModelLabel(config ModelPopupConfig, presets []ModelPopupPreset) string {
	current := strings.TrimSpace(config.CurrentModel)
	for _, preset := range presets {
		if strings.TrimSpace(preset.Model) == current {
			return strings.TrimSpace(preset.Model)
		}
	}
	if strings.TrimSpace(config.CurrentLabel) != "" {
		return strings.TrimSpace(config.CurrentLabel)
	}
	if current != "" {
		return current
	}
	return "unknown"
}

func modelPopupWarningLines(config ModelPopupConfig) []string {
	baseURL := strings.TrimSpace(config.CustomOpenAIBaseURL)
	if baseURL == "" || strings.TrimRight(baseURL, "/") == DefaultOpenAIBaseURLForModels {
		return nil
	}
	return []string{"Warning: OpenAI base URL is overridden to " + baseURL + ". Selecting models may not be supported or work properly."}
}

func effectiveReasoningPopupOptions(preset ModelPopupPreset) []ReasoningEffortPopupOption {
	options := make([]ReasoningEffortPopupOption, 0, len(preset.SupportedReasoningEfforts))
	seen := map[string]bool{}
	for _, option := range preset.SupportedReasoningEfforts {
		effort := strings.TrimSpace(option.Effort)
		if effort == "" || seen[effort] {
			continue
		}
		seen[effort] = true
		option.Effort = effort
		options = append(options, option)
	}
	if len(options) == 0 && strings.TrimSpace(preset.DefaultReasoningEffort) != "" {
		options = append(options, ReasoningEffortPopupOption{Effort: strings.TrimSpace(preset.DefaultReasoningEffort)})
	}
	return options
}

func reasoningOptionsContain(options []ReasoningEffortPopupOption, effort string) bool {
	effort = strings.TrimSpace(effort)
	for _, option := range options {
		if strings.TrimSpace(option.Effort) == effort {
			return true
		}
	}
	return false
}

func reasoningSelectedDescription(modelID string, effort string, description string) string {
	warning := reasoningWarningForModel(modelID, effort)
	description = strings.TrimSpace(description)
	switch {
	case warning == "":
		return ""
	case description == "":
		return warning
	default:
		return description + "\n" + warning
	}
}

func reasoningWarningForModel(modelID string, effort string) string {
	modelID = strings.TrimSpace(modelID)
	effort = strings.ToLower(strings.TrimSpace(effort))
	if !(strings.HasPrefix(modelID, "gpt-5.1-codex") || strings.HasPrefix(modelID, "gpt-5.1-codex-max") || strings.HasPrefix(modelID, "gpt-5.2")) {
		return ""
	}
	if effort == "high" || effort == "xhigh" || effort == "x-high" || effort == "extra-high" || effort == "extra_high" {
		return ReasoningEffortPopupLabel(effort) + " reasoning effort can quickly consume Plus plan rate limits."
	}
	return ""
}

func reasoningEffortSentenceLabelPopup(effort string) string {
	label := ReasoningEffortPopupLabel(effort)
	if label == "Default" {
		return "the selected"
	}
	return strings.ToLower(label)
}

func selectionItemsHaveCurrent(items []SelectionItem) bool {
	for _, item := range items {
		if item.IsCurrent {
			return true
		}
	}
	return false
}
