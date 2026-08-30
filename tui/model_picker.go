package tui

import (
	"strings"

	"codex_go/model"
)

// Rust parity: codex-rs/tui/src/chatwidget/model_popups.rs and
// codex-rs/tui/src/bottom_pane/list_selection_view.rs.

type ModelPickerOption struct {
	ID                        string
	Label                     string
	Description               string
	IsCurrent                 bool
	IsDefault                 bool
	DefaultReasoningEffort    string
	SupportedReasoningEfforts []ReasoningEffortOption
	ServiceTiers              []string
}

type ReasoningEffortOption struct {
	Effort      string
	Label       string
	Description string
	IsDefault   bool
	IsCurrent   bool
}

type PlanReasoningScope string

const (
	PlanReasoningScopePlanOnly PlanReasoningScope = "plan_only"
	PlanReasoningScopeAllModes PlanReasoningScope = "all_modes"
)

const (
	PlanReasoningScopeTitle        = "Apply reasoning change"
	PlanReasoningScopePlanOnlyText = "Apply to Plan mode override"
	PlanReasoningScopeAllModesText = "Apply to global default and Plan mode override"
)

type PlanReasoningScopeOption struct {
	Scope       PlanReasoningScope
	Label       string
	Description string
}

type ModelPicker struct {
	Options  []ModelPickerOption
	Selected int
	Current  string
}

func BundledModelPickerOptions() []ModelPickerOption {
	manager := model.NewStaticModelsManager(model.BundledModelsResponse())
	return ModelPickerOptionsFromPresets(manager.ListModels(model.RefreshOffline))
}

func ModelPickerOptionsFromCatalog(catalog model.ModelsResponse) []ModelPickerOption {
	manager := model.NewStaticModelsManager(catalog)
	return ModelPickerOptionsFromPresets(manager.ListModels(model.RefreshOffline))
}

func ModelPickerOptionsFromPresets(presets []model.ModelPreset) []ModelPickerOption {
	options := make([]ModelPickerOption, 0, len(presets))
	for _, preset := range presets {
		id := strings.TrimSpace(preset.Model)
		if id == "" || !modelPresetShowsInPicker(preset.Visibility) {
			continue
		}
		label := strings.TrimSpace(preset.Name)
		if label == "" {
			label = id
		}
		options = append(options, ModelPickerOption{
			ID:                        id,
			Label:                     label,
			Description:               strings.TrimSpace(preset.Description),
			IsDefault:                 preset.IsDefault,
			DefaultReasoningEffort:    strings.TrimSpace(preset.DefaultReasoningLevel),
			SupportedReasoningEfforts: reasoningOptionsForModelPreset(preset),
			ServiceTiers:              append([]string(nil), preset.ServiceTiers...),
		})
	}
	return options
}

func NewModelPicker(options []ModelPickerOption, current string) *ModelPicker {
	current = strings.TrimSpace(current)
	picker := &ModelPicker{
		Options: normalizeModelPickerOptions(options, current),
		Current: current,
	}
	for i, option := range picker.Options {
		if option.ID == current {
			picker.Selected = i
			return picker
		}
	}
	for i, option := range picker.Options {
		if option.IsDefault {
			picker.Selected = i
			return picker
		}
	}
	return picker
}

func (p *ModelPicker) Move(delta int) {
	if p == nil || len(p.Options) == 0 {
		return
	}
	p.Selected = (p.Selected + delta) % len(p.Options)
	if p.Selected < 0 {
		p.Selected += len(p.Options)
	}
}

func (p *ModelPicker) Select(index int) {
	if p == nil || index < 0 || index >= len(p.Options) {
		return
	}
	p.Selected = index
}

func (p *ModelPicker) SelectedModel() (ModelPickerOption, bool) {
	if p == nil || len(p.Options) == 0 || p.Selected < 0 || p.Selected >= len(p.Options) {
		return ModelPickerOption{}, false
	}
	return p.Options[p.Selected], true
}

func (p *ModelPicker) OptionByID(id string) (ModelPickerOption, bool) {
	id = strings.TrimSpace(id)
	if p == nil || id == "" {
		return ModelPickerOption{}, false
	}
	for _, option := range p.Options {
		if option.ID == id {
			return option, true
		}
	}
	return ModelPickerOption{}, false
}

func (p *ModelPicker) RenderRows(width int) []string {
	if p == nil || len(p.Options) == 0 {
		return []string{"No models available"}
	}
	items := make([]SelectionItem, 0, len(p.Options))
	for _, option := range p.Options {
		description := option.Description
		markers := []string{}
		if option.IsCurrent {
			markers = append(markers, "current")
		}
		if option.IsDefault {
			markers = append(markers, "default")
		}
		if len(markers) > 0 {
			if description != "" {
				description += " "
			}
			description += "(" + strings.Join(markers, ", ") + ")"
		}
		items = append(items, SelectionItem{
			ID:          option.ID,
			Label:       option.Label,
			Description: description,
		})
	}
	list := &SelectionList{Items: items, Selected: p.Selected}
	return list.RenderRows(width)
}

func normalizeModelPickerOptions(options []ModelPickerOption, current string) []ModelPickerOption {
	out := make([]ModelPickerOption, 0, len(options))
	seen := map[string]bool{}
	for _, option := range options {
		id := strings.TrimSpace(option.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		label := strings.TrimSpace(option.Label)
		if label == "" {
			label = id
		}
		out = append(out, ModelPickerOption{
			ID:                        id,
			Label:                     label,
			Description:               strings.TrimSpace(option.Description),
			IsCurrent:                 id == current,
			IsDefault:                 option.IsDefault,
			DefaultReasoningEffort:    strings.TrimSpace(option.DefaultReasoningEffort),
			SupportedReasoningEfforts: cloneReasoningEffortOptions(option.SupportedReasoningEfforts),
		})
	}
	return out
}

func (o ModelPickerOption) EffectiveReasoningEfforts() []ReasoningEffortOption {
	efforts := cloneReasoningEffortOptions(o.SupportedReasoningEfforts)
	defaultEffort := strings.TrimSpace(o.DefaultReasoningEffort)
	if len(efforts) == 0 && defaultEffort != "" {
		efforts = []ReasoningEffortOption{{Effort: defaultEffort, Label: ReasoningEffortLabel(defaultEffort), IsDefault: true}}
	}
	if defaultEffort == "" && len(efforts) > 0 {
		defaultEffort = efforts[0].Effort
	}
	for i := range efforts {
		if efforts[i].Effort == defaultEffort {
			efforts[i].IsDefault = true
		}
		if efforts[i].Label == "" {
			efforts[i].Label = ReasoningEffortLabel(efforts[i].Effort)
		}
	}
	return efforts
}

func (o ModelPickerOption) NeedsReasoningPicker() bool {
	return len(o.EffectiveReasoningEfforts()) > 1
}

func (o ModelPickerOption) DefaultReasoning() string {
	defaultEffort := strings.TrimSpace(o.DefaultReasoningEffort)
	if defaultEffort != "" {
		return defaultEffort
	}
	efforts := o.EffectiveReasoningEfforts()
	if len(efforts) == 0 {
		return ""
	}
	return efforts[0].Effort
}

type ModelReasoningPicker struct {
	Model    ModelPickerOption
	Options  []ReasoningEffortOption
	Selected int
	Current  string
}

type PlanReasoningScopePicker struct {
	Model    ModelPickerOption
	Effort   string
	Options  []PlanReasoningScopeOption
	Selected int
}

func NewModelReasoningPicker(modelOption ModelPickerOption, current string) *ModelReasoningPicker {
	current = strings.TrimSpace(current)
	options := modelOption.EffectiveReasoningEfforts()
	for i := range options {
		options[i].IsCurrent = options[i].Effort == current && current != ""
		if options[i].Label == "" {
			options[i].Label = ReasoningEffortLabel(options[i].Effort)
		}
	}
	picker := &ModelReasoningPicker{
		Model:   modelOption,
		Options: options,
		Current: current,
	}
	for i, option := range picker.Options {
		if option.IsCurrent {
			picker.Selected = i
			return picker
		}
	}
	for i, option := range picker.Options {
		if option.IsDefault {
			picker.Selected = i
			return picker
		}
	}
	return picker
}

func (p *ModelReasoningPicker) Select(index int) {
	if p == nil || index < 0 || index >= len(p.Options) {
		return
	}
	p.Selected = index
}

func (p *ModelReasoningPicker) Move(delta int) {
	if p == nil || len(p.Options) == 0 {
		return
	}
	p.Selected = (p.Selected + delta) % len(p.Options)
	if p.Selected < 0 {
		p.Selected += len(p.Options)
	}
}

func (p *ModelReasoningPicker) SelectedEffort() (ReasoningEffortOption, bool) {
	if p == nil || len(p.Options) == 0 || p.Selected < 0 || p.Selected >= len(p.Options) {
		return ReasoningEffortOption{}, false
	}
	return p.Options[p.Selected], true
}

func (p *ModelReasoningPicker) EffortByID(id string) (ReasoningEffortOption, bool) {
	id = strings.TrimSpace(id)
	if p == nil || id == "" {
		return ReasoningEffortOption{}, false
	}
	for _, option := range p.Options {
		if option.Effort == id {
			return option, true
		}
	}
	return ReasoningEffortOption{}, false
}

func NewPlanReasoningScopePicker(modelOption ModelPickerOption, effort string, currentPlanEffort string) *PlanReasoningScopePicker {
	effort = strings.TrimSpace(effort)
	reasoningPhrase := reasoningEffortSentencePhrase(effort)
	planReasoningSource := "built-in Plan default"
	if currentPlanEffort = strings.TrimSpace(currentPlanEffort); currentPlanEffort != "" {
		planReasoningSource = "user-chosen Plan override (" + reasoningEffortSentenceLabel(currentPlanEffort) + ")"
	}
	return &PlanReasoningScopePicker{
		Model:  modelOption,
		Effort: effort,
		Options: []PlanReasoningScopeOption{
			{
				Scope:       PlanReasoningScopePlanOnly,
				Label:       PlanReasoningScopePlanOnlyText,
				Description: "Always use " + reasoningPhrase + " in Plan mode.",
			},
			{
				Scope:       PlanReasoningScopeAllModes,
				Label:       PlanReasoningScopeAllModesText,
				Description: "Set the global default reasoning level and the Plan mode override. This replaces the current " + planReasoningSource + ".",
			},
		},
	}
}

func (p *PlanReasoningScopePicker) Select(index int) {
	if p == nil || index < 0 || index >= len(p.Options) {
		return
	}
	p.Selected = index
}

func (p *PlanReasoningScopePicker) Move(delta int) {
	if p == nil || len(p.Options) == 0 {
		return
	}
	p.Selected = (p.Selected + delta) % len(p.Options)
	if p.Selected < 0 {
		p.Selected += len(p.Options)
	}
}

func (p *PlanReasoningScopePicker) OptionByID(id string) (PlanReasoningScopeOption, bool) {
	id = strings.TrimSpace(id)
	if p == nil || id == "" {
		return PlanReasoningScopeOption{}, false
	}
	for _, option := range p.Options {
		if string(option.Scope) == id {
			return option, true
		}
	}
	return PlanReasoningScopeOption{}, false
}

func ReasoningEffortLabel(effort string) string {
	switch strings.TrimSpace(strings.ToLower(effort)) {
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
	case "xhigh", "x-high", "extra_high", "extra-high":
		return "Extra high"
	case "ultra":
		return "Ultra"
	default:
		if strings.TrimSpace(effort) == "" {
			return "Default"
		}
		return strings.TrimSpace(effort)
	}
}

func reasoningEffortSentencePhrase(effort string) string {
	effort = strings.TrimSpace(effort)
	if effort == "" {
		return "the selected reasoning"
	}
	if strings.EqualFold(effort, "none") {
		return "no reasoning"
	}
	return reasoningEffortSentenceLabel(effort) + " reasoning"
}

func reasoningEffortSentenceLabel(effort string) string {
	label := ReasoningEffortLabel(effort)
	if label == strings.TrimSpace(effort) {
		return label
	}
	return strings.ToLower(label)
}

func reasoningOptionsForModelPreset(preset model.ModelPreset) []ReasoningEffortOption {
	levels := append([]string(nil), preset.SupportedReasoningLevels...)
	if len(levels) == 0 && strings.TrimSpace(preset.DefaultReasoningLevel) != "" {
		levels = []string{preset.DefaultReasoningLevel}
	}
	out := make([]ReasoningEffortOption, 0, len(levels))
	seen := map[string]bool{}
	defaultEffort := strings.TrimSpace(preset.DefaultReasoningLevel)
	for _, level := range levels {
		level = strings.TrimSpace(level)
		if level == "" || seen[level] {
			continue
		}
		seen[level] = true
		out = append(out, ReasoningEffortOption{
			Effort:      level,
			Label:       ReasoningEffortLabel(level),
			Description: ReasoningEffortLabel(level) + " reasoning",
			IsDefault:   level == defaultEffort,
		})
	}
	return out
}

func cloneReasoningEffortOptions(options []ReasoningEffortOption) []ReasoningEffortOption {
	if options == nil {
		return nil
	}
	out := make([]ReasoningEffortOption, len(options))
	copy(out, options)
	return out
}

func modelPresetShowsInPicker(visibility string) bool {
	switch strings.TrimSpace(strings.ToLower(visibility)) {
	case model.VisibilityVisible, model.VisibilityList:
		return true
	default:
		return false
	}
}
