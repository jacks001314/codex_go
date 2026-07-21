package model

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const (
	BaseInstructions = `You are Codex, a coding agent based on GPT-5.

## Responsiveness

### Preamble messages

Before making tool calls, send a brief preamble to the user explaining what you are about to do.

- Logically group related actions: if you are about to run several related commands, describe them together in one preamble rather than sending a separate note for each.
- Keep it concise: use no more than 1-2 sentences focused on the immediate, tangible next steps.
- Build on prior context: after earlier work, briefly connect what was learned to what you will do next.
- Keep the tone collaborative and direct.
- Avoid a preamble only for a truly trivial read that is not part of a larger action.

The message before a tool call must describe what is immediately about to happen. For example: "I will query Beijing's live weather and today's forecast."

### Progress updates

During longer work, send short progress updates at meaningful points. Do not wait until after all tool calls to explain what you did. Commentary and progress messages must appear before the tool calls they introduce.`

	TruncationModeBytes  = "bytes"
	TruncationModeTokens = "tokens"

	ServiceTierDefaultRequestValue = "default"

	VisibilityNone    = "none"
	VisibilityHide    = "hide"
	VisibilityList    = "list"
	VisibilityVisible = "visible"
)

const personalityPlaceholder = "{{ personality }}"

type ModelMessages struct {
	InstructionsTemplate string `json:"instructions_template,omitempty"`
	PersonalityDefault   string `json:"-"`
	PersonalityFriendly  string `json:"-"`
	PersonalityPragmatic string `json:"-"`
}

func (m *ModelMessages) UnmarshalJSON(data []byte) error {
	var raw struct {
		InstructionsTemplate  string            `json:"instructions_template"`
		InstructionsVariables map[string]string `json:"instructions_variables"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	m.InstructionsTemplate = raw.InstructionsTemplate
	if raw.InstructionsVariables != nil {
		m.PersonalityDefault = raw.InstructionsVariables["personality_default"]
		m.PersonalityFriendly = raw.InstructionsVariables["personality_friendly"]
		m.PersonalityPragmatic = raw.InstructionsVariables["personality_pragmatic"]
	}
	return nil
}

func (m *ModelMessages) SupportsPersonality() bool {
	if m == nil || !strings.Contains(m.InstructionsTemplate, personalityPlaceholder) {
		return false
	}
	return m.PersonalityFriendly != "" && m.PersonalityPragmatic != ""
}

func (m *ModelMessages) PersonalityMessage(personality string) (string, bool) {
	if m == nil {
		return "", false
	}
	switch strings.ToLower(strings.TrimSpace(personality)) {
	case "none":
		return "", true
	case "friendly":
		if m.PersonalityFriendly != "" {
			return m.PersonalityFriendly, true
		}
	case "pragmatic":
		if m.PersonalityPragmatic != "" {
			return m.PersonalityPragmatic, true
		}
	case "":
		return m.PersonalityDefault, true
	default:
		return m.PersonalityDefault, true
	}
	return "", false
}

func (m *ModelInfo) SupportsPersonality() bool {
	return m != nil && m.ModelMessages != nil && m.ModelMessages.SupportsPersonality()
}

func (m *ModelInfo) PersonalityMessage(personality string) (string, bool) {
	if m == nil || m.ModelMessages == nil {
		return "", false
	}
	return m.ModelMessages.PersonalityMessage(personality)
}

func (m *ModelInfo) ModelInstructions(personality string) string {
	if m == nil {
		return BaseInstructions
	}
	if m.ModelMessages != nil && strings.TrimSpace(m.ModelMessages.InstructionsTemplate) != "" {
		message, _ := m.ModelMessages.PersonalityMessage(personality)
		return strings.ReplaceAll(m.ModelMessages.InstructionsTemplate, personalityPlaceholder, message)
	}
	return m.BaseInstructions
}

type TruncationPolicy struct {
	Mode  string `json:"mode"`
	Limit int64  `json:"limit"`
}

type ModelInfo struct {
	Slug                           string           `json:"slug"`
	DisplayName                    string           `json:"display_name"`
	Description                    string           `json:"description"`
	DefaultReasoningLevel          string           `json:"default_reasoning_level"`
	SupportedReasoningLevels       []string         `json:"supported_reasoning_levels"`
	Visibility                     string           `json:"visibility"`
	SupportedInAPI                 bool             `json:"supported_in_api"`
	Priority                       int              `json:"priority"`
	AdditionalSpeedTiers           []string         `json:"additional_speed_tiers"`
	ServiceTiers                   []string         `json:"service_tiers"`
	DefaultServiceTier             string           `json:"default_service_tier"`
	BaseInstructions               string           `json:"base_instructions"`
	ModelMessages                  *ModelMessages   `json:"model_messages"`
	IncludeSkillsUsageInstructions bool             `json:"include_skills_usage_instructions"`
	SupportsReasoningSummaries     bool             `json:"supports_reasoning_summaries"`
	DefaultReasoningSummary        string           `json:"default_reasoning_summary"`
	SupportVerbosity               bool             `json:"support_verbosity"`
	DefaultVerbosity               string           `json:"default_verbosity"`
	WebSearchToolType              string           `json:"web_search_tool_type"`
	TruncationPolicy               TruncationPolicy `json:"truncation_policy"`
	SupportsParallelToolCalls      bool             `json:"supports_parallel_tool_calls"`
	SupportsImageDetailOriginal    bool             `json:"supports_image_detail_original"`
	ContextWindow                  int64            `json:"context_window"`
	MaxContextWindow               int64            `json:"max_context_window"`
	AutoCompactTokenLimit          int64            `json:"auto_compact_token_limit"`
	EffectiveContextWindowPercent  int              `json:"effective_context_window_percent"`
	InputModalities                []string         `json:"input_modalities"`
	UsedFallbackModelMetadata      bool             `json:"-"`
	SupportsSearchTool             bool             `json:"supports_search_tool"`
	UseResponsesLite               bool             `json:"use_responses_lite"`
	AutoReviewModelOverride        string           `json:"auto_review_model_override"`
}

func (m *ModelInfo) UnmarshalJSON(data []byte) error {
	type rawTruncationPolicy struct {
		Mode  string `json:"mode"`
		Limit int64  `json:"limit"`
	}
	var raw struct {
		Slug                           string              `json:"slug"`
		DisplayName                    string              `json:"display_name"`
		Description                    any                 `json:"description"`
		DefaultReasoningLevel          any                 `json:"default_reasoning_level"`
		SupportedReasoningLevels       []json.RawMessage   `json:"supported_reasoning_levels"`
		Visibility                     string              `json:"visibility"`
		SupportedInAPI                 bool                `json:"supported_in_api"`
		Priority                       int                 `json:"priority"`
		AdditionalSpeedTiers           []string            `json:"additional_speed_tiers"`
		ServiceTiers                   []json.RawMessage   `json:"service_tiers"`
		DefaultServiceTier             any                 `json:"default_service_tier"`
		BaseInstructions               string              `json:"base_instructions"`
		ModelMessages                  *ModelMessages      `json:"model_messages"`
		IncludeSkillsUsageInstructions bool                `json:"include_skills_usage_instructions"`
		SupportsReasoningSummaries     bool                `json:"supports_reasoning_summaries"`
		DefaultReasoningSummary        string              `json:"default_reasoning_summary"`
		SupportVerbosity               bool                `json:"support_verbosity"`
		DefaultVerbosity               any                 `json:"default_verbosity"`
		WebSearchToolType              string              `json:"web_search_tool_type"`
		TruncationPolicy               rawTruncationPolicy `json:"truncation_policy"`
		SupportsParallelToolCalls      bool                `json:"supports_parallel_tool_calls"`
		SupportsImageDetailOriginal    bool                `json:"supports_image_detail_original"`
		ContextWindow                  int64               `json:"context_window"`
		MaxContextWindow               int64               `json:"max_context_window"`
		AutoCompactTokenLimit          int64               `json:"auto_compact_token_limit"`
		EffectiveContextWindowPercent  int                 `json:"effective_context_window_percent"`
		InputModalities                []string            `json:"input_modalities"`
		SupportsSearchTool             bool                `json:"supports_search_tool"`
		UseResponsesLite               bool                `json:"use_responses_lite"`
		AutoReviewModelOverride        any                 `json:"auto_review_model_override"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*m = ModelInfo{
		Slug:                           raw.Slug,
		DisplayName:                    raw.DisplayName,
		Description:                    stringFromJSONValue(raw.Description),
		DefaultReasoningLevel:          stringFromJSONValue(raw.DefaultReasoningLevel),
		SupportedReasoningLevels:       reasoningLevelsFromJSON(raw.SupportedReasoningLevels),
		Visibility:                     raw.Visibility,
		SupportedInAPI:                 raw.SupportedInAPI,
		Priority:                       raw.Priority,
		AdditionalSpeedTiers:           cloneStrings(raw.AdditionalSpeedTiers),
		ServiceTiers:                   serviceTierIDsFromJSON(raw.ServiceTiers),
		DefaultServiceTier:             stringFromJSONValue(raw.DefaultServiceTier),
		BaseInstructions:               raw.BaseInstructions,
		ModelMessages:                  raw.ModelMessages,
		IncludeSkillsUsageInstructions: raw.IncludeSkillsUsageInstructions,
		SupportsReasoningSummaries:     raw.SupportsReasoningSummaries,
		DefaultReasoningSummary:        raw.DefaultReasoningSummary,
		SupportVerbosity:               raw.SupportVerbosity,
		DefaultVerbosity:               stringFromJSONValue(raw.DefaultVerbosity),
		WebSearchToolType:              raw.WebSearchToolType,
		TruncationPolicy:               TruncationPolicy(raw.TruncationPolicy),
		SupportsParallelToolCalls:      raw.SupportsParallelToolCalls,
		SupportsImageDetailOriginal:    raw.SupportsImageDetailOriginal,
		ContextWindow:                  raw.ContextWindow,
		MaxContextWindow:               raw.MaxContextWindow,
		AutoCompactTokenLimit:          raw.AutoCompactTokenLimit,
		EffectiveContextWindowPercent:  raw.EffectiveContextWindowPercent,
		InputModalities:                cloneStrings(raw.InputModalities),
		SupportsSearchTool:             raw.SupportsSearchTool,
		UseResponsesLite:               raw.UseResponsesLite,
		AutoReviewModelOverride:        stringFromJSONValue(raw.AutoReviewModelOverride),
	}
	if m.BaseInstructions == "" {
		m.BaseInstructions = BaseInstructions
	}
	if m.EffectiveContextWindowPercent == 0 {
		m.EffectiveContextWindowPercent = 95
	}
	if len(m.InputModalities) == 0 {
		m.InputModalities = []string{"text", "image"}
	}
	return nil
}

type ModelsResponse struct {
	Models []ModelInfo `json:"models"`
}

type ModelPreset struct {
	Model                    string
	Name                     string
	Description              string
	IsDefault                bool
	Priority                 int
	Visibility               string
	DefaultReasoningLevel    string
	SupportedReasoningLevels []string
}

type ModelsManagerConfig struct {
	ModelContextWindow              int64
	ModelAutoCompactTokenLimit      int64
	ToolOutputTokenLimit            int64
	BaseInstructions                string
	PersonalityEnabled              bool
	ModelSupportsReasoningSummaries *bool
	ModelCatalog                    *ModelsResponse
}

type RefreshStrategy string

const (
	RefreshOnline           RefreshStrategy = "online"
	RefreshOffline          RefreshStrategy = "offline"
	RefreshOnlineIfUncached RefreshStrategy = "online_if_uncached"
)

type ModelsManager interface {
	ListModels(strategy RefreshStrategy) []ModelPreset
	RawModelCatalog(strategy RefreshStrategy) ModelsResponse
	GetRemoteModels() []ModelInfo
	GetDefaultModel(model string, allowProviderModelFallback bool, strategy RefreshStrategy) string
	GetModelInfo(model string, config *ModelsManagerConfig) ModelInfo
	RefreshIfNewETag(etag string)
}

type StaticModelsManager struct {
	remoteModels []ModelInfo
}

func NewStaticModelsManager(modelCatalog ModelsResponse) *StaticModelsManager {
	return &StaticModelsManager{remoteModels: cloneModelInfos(modelCatalog.Models)}
}

func (m *StaticModelsManager) ListModels(strategy RefreshStrategy) []ModelPreset {
	return BuildAvailableModels(m.RawModelCatalog(strategy).Models)
}

func (m *StaticModelsManager) RawModelCatalog(_ RefreshStrategy) ModelsResponse {
	return ModelsResponse{Models: m.GetRemoteModels()}
}

func (m *StaticModelsManager) GetRemoteModels() []ModelInfo {
	return cloneModelInfos(m.remoteModels)
}

func (m *StaticModelsManager) GetDefaultModel(model string, allowProviderModelFallback bool, strategy RefreshStrategy) string {
	availableModels := m.ListModels(strategy)
	if allowProviderModelFallback {
		if requestedModelIsAvailable(model, availableModels) {
			return model
		}
		return defaultModelFromAvailable(availableModels)
	}
	if model != "" {
		return model
	}
	return defaultModelFromAvailable(availableModels)
}

func (m *StaticModelsManager) GetModelInfo(model string, config *ModelsManagerConfig) ModelInfo {
	return ConstructModelInfoFromCandidates(model, m.GetRemoteModels(), config)
}

func (m *StaticModelsManager) RefreshIfNewETag(_ string) {}

func BundledModelsResponse() ModelsResponse {
	if catalog, err := loadBundledModelsResponse(); err == nil && len(catalog.Models) > 0 {
		return catalog
	}
	return fallbackBundledModelsResponse()
}

func fallbackBundledModelsResponse() ModelsResponse {
	return ModelsResponse{
		Models: []ModelInfo{
			{
				Slug: "gpt-5.6-sol", DisplayName: "GPT-5.6-Sol", Description: "Latest frontier agentic coding model.",
				Visibility: VisibilityVisible, SupportedInAPI: true, Priority: 1, BaseInstructions: BaseInstructions,
				ContextWindow: 372000, MaxContextWindow: 372000, EffectiveContextWindowPercent: 95,
				InputModalities: []string{"text", "image"}, SupportsParallelToolCalls: true,
			},
			{
				Slug: "gpt-5.6-terra", DisplayName: "GPT-5.6-Terra", Description: "Frontier coding model.",
				Visibility: VisibilityVisible, SupportedInAPI: true, Priority: 2, BaseInstructions: BaseInstructions,
				ContextWindow: 372000, MaxContextWindow: 372000, EffectiveContextWindowPercent: 95,
				InputModalities: []string{"text", "image"}, SupportsParallelToolCalls: true,
			},
			{
				Slug: "gpt-5.6-luna", DisplayName: "GPT-5.6-Luna", Description: "Frontier coding model.",
				Visibility: VisibilityVisible, SupportedInAPI: true, Priority: 3, BaseInstructions: BaseInstructions,
				ContextWindow: 372000, MaxContextWindow: 372000, EffectiveContextWindowPercent: 95,
				InputModalities: []string{"text", "image"}, SupportsParallelToolCalls: true,
			},
			{
				Slug:                           "gpt-5.5",
				DisplayName:                    "gpt-5.5",
				Description:                    "Frontier model for complex coding and research.",
				Visibility:                     VisibilityVisible,
				SupportedInAPI:                 true,
				Priority:                       0,
				ServiceTiers:                   []string{"default", "priority"},
				DefaultServiceTier:             "default",
				BaseInstructions:               BaseInstructions,
				IncludeSkillsUsageInstructions: true,
				TruncationPolicy:               TruncationPolicy{Mode: TruncationModeBytes, Limit: 10000},
				ContextWindow:                  272000,
				MaxContextWindow:               272000,
				EffectiveContextWindowPercent:  95,
				InputModalities:                []string{"text", "image"},
			},
			{
				Slug: "gpt-5.2", DisplayName: "gpt-5.2", Description: "General purpose coding model.",
				Visibility: VisibilityVisible, SupportedInAPI: true, Priority: 40, BaseInstructions: BaseInstructions,
				ContextWindow: 272000, MaxContextWindow: 272000, EffectiveContextWindowPercent: 95,
				InputModalities: []string{"text", "image"},
			},
			{
				Slug:                           "gpt-5.4",
				DisplayName:                    "gpt-5.4",
				Description:                    "Strong model for everyday coding.",
				Visibility:                     VisibilityVisible,
				SupportedInAPI:                 true,
				Priority:                       10,
				BaseInstructions:               BaseInstructions,
				IncludeSkillsUsageInstructions: true,
				TruncationPolicy:               TruncationPolicy{Mode: TruncationModeBytes, Limit: 10000},
				ContextWindow:                  272000,
				MaxContextWindow:               272000,
				EffectiveContextWindowPercent:  95,
				InputModalities:                []string{"text", "image"},
			},
			{
				Slug:                           "gpt-5.4-mini",
				DisplayName:                    "gpt-5.4-mini",
				Description:                    "Small, fast model for simpler coding tasks.",
				Visibility:                     VisibilityVisible,
				SupportedInAPI:                 true,
				Priority:                       20,
				BaseInstructions:               BaseInstructions,
				IncludeSkillsUsageInstructions: true,
				TruncationPolicy:               TruncationPolicy{Mode: TruncationModeBytes, Limit: 10000},
				ContextWindow:                  128000,
				MaxContextWindow:               128000,
				EffectiveContextWindowPercent:  95,
				InputModalities:                []string{"text"},
			},
			{
				Slug:                           "gpt-5.3-codex",
				DisplayName:                    "gpt-5.3-codex",
				Description:                    "Coding-optimized model.",
				Visibility:                     VisibilityVisible,
				SupportedInAPI:                 true,
				Priority:                       30,
				BaseInstructions:               BaseInstructions,
				IncludeSkillsUsageInstructions: true,
				TruncationPolicy:               TruncationPolicy{Mode: TruncationModeBytes, Limit: 10000},
				ContextWindow:                  272000,
				MaxContextWindow:               272000,
				EffectiveContextWindowPercent:  95,
				InputModalities:                []string{"text", "image"},
			},
		},
	}
}

func AmazonBedrockModelCatalog() ModelsResponse {
	return ModelsResponse{
		Models: []ModelInfo{
			bedrockModel(AmazonBedrockGPT55ModelID, 0),
			bedrockModel(AmazonBedrockGPT54ModelID, 10),
			bedrockModel(AmazonBedrockGPT56SolModelID, 20),
			bedrockModel(AmazonBedrockGPT56TerraModelID, 30),
			bedrockModel(AmazonBedrockGPT56LunaModelID, 40),
		},
	}
}

func WithDefaultOnlyServiceTier(catalog ModelsResponse) ModelsResponse {
	models := cloneModelInfos(catalog.Models)
	for i := range models {
		models[i].AdditionalSpeedTiers = nil
		models[i].ServiceTiers = nil
		models[i].DefaultServiceTier = ""
	}
	return ModelsResponse{Models: models}
}

func BuildAvailableModels(remoteModels []ModelInfo) []ModelPreset {
	models := cloneModelInfos(remoteModels)
	sort.SliceStable(models, func(i, j int) bool {
		return models[i].Priority < models[j].Priority
	})
	presets := make([]ModelPreset, 0, len(models))
	for _, model := range models {
		if !modelVisibleInCatalog(model.Visibility) || !model.SupportedInAPI {
			continue
		}
		presets = append(presets, ModelPreset{
			Model:                    model.Slug,
			Name:                     model.DisplayName,
			Description:              model.Description,
			Priority:                 model.Priority,
			Visibility:               model.Visibility,
			DefaultReasoningLevel:    model.DefaultReasoningLevel,
			SupportedReasoningLevels: cloneStrings(model.SupportedReasoningLevels),
		})
	}
	if len(presets) > 0 {
		markDefaultPresetByVisibility(presets)
	}
	return presets
}

func loadBundledModelsResponse() (ModelsResponse, error) {
	for _, path := range bundledModelCatalogPaths() {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var catalog ModelsResponse
		if err := json.Unmarshal(data, &catalog); err != nil {
			return ModelsResponse{}, err
		}
		if len(catalog.Models) > 0 {
			return catalog, nil
		}
	}
	return ModelsResponse{}, os.ErrNotExist
}

func bundledModelCatalogPaths() []string {
	seen := map[string]bool{}
	out := []string{}
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		clean := filepath.Clean(path)
		if !seen[clean] {
			seen[clean] = true
			out = append(out, clean)
		}
	}
	add(os.Getenv("CODEX_GO_MODELS_JSON"))
	add(filepath.Join("models-manager", "models.json"))
	// Development checkout layout: keep the Go TUI in sync with the sibling
	// Rust catalog when both repositories are present.
	add(filepath.Join("..", "git", "codex", "codex-rs", "models-manager", "models.json"))
	add(filepath.Join("..", "codex-main", "codex-rs", "models-manager", "models.json"))
	if _, file, _, ok := runtime.Caller(0); ok {
		dir := filepath.Dir(file)
		for current := dir; current != ""; current = filepath.Dir(current) {
			add(filepath.Join(current, "models-manager", "models.json"))
			add(filepath.Join(current, "..", "codex-main", "codex-rs", "models-manager", "models.json"))
			next := filepath.Dir(current)
			if next == current {
				break
			}
		}
	}
	return out
}

func stringFromJSONValue(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func reasoningLevelsFromJSON(values []json.RawMessage) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		var level string
		if err := json.Unmarshal(value, &level); err == nil {
			level = strings.TrimSpace(level)
		}
		if level == "" {
			var object map[string]any
			if err := json.Unmarshal(value, &object); err == nil {
				level = stringFromJSONValue(object["effort"])
			}
		}
		if level != "" {
			out = append(out, level)
		}
	}
	return out
}

func serviceTierIDsFromJSON(values []json.RawMessage) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		var id string
		if err := json.Unmarshal(value, &id); err == nil {
			id = strings.TrimSpace(id)
		}
		if id == "" {
			var object map[string]any
			if err := json.Unmarshal(value, &object); err == nil {
				id = stringFromJSONValue(object["id"])
			}
		}
		if id != "" {
			out = append(out, id)
		}
	}
	return out
}

func modelVisibleInCatalog(visibility string) bool {
	visibility = strings.TrimSpace(strings.ToLower(visibility))
	return visibility == VisibilityVisible || visibility == VisibilityList || visibility == VisibilityHide
}

func modelVisibleInPicker(visibility string) bool {
	visibility = strings.TrimSpace(strings.ToLower(visibility))
	return visibility == VisibilityVisible || visibility == VisibilityList
}

func markDefaultPresetByVisibility(presets []ModelPreset) {
	for i := range presets {
		presets[i].IsDefault = false
	}
	for i := range presets {
		if modelVisibleInPicker(presets[i].Visibility) {
			presets[i].IsDefault = true
			return
		}
	}
	if len(presets) > 0 {
		presets[0].IsDefault = true
	}
}

func modelHiddenFromPicker(visibility string) bool {
	return !modelVisibleInPicker(visibility)
}

func ModelInfoFromSlug(slug string) ModelInfo {
	return ModelInfo{
		Slug:                           slug,
		DisplayName:                    slug,
		Visibility:                     VisibilityNone,
		SupportedInAPI:                 true,
		Priority:                       99,
		BaseInstructions:               BaseInstructions,
		ModelMessages:                  localPersonalityMessagesForSlug(slug),
		IncludeSkillsUsageInstructions: true,
		DefaultReasoningSummary:        "auto",
		WebSearchToolType:              "text",
		TruncationPolicy:               TruncationPolicy{Mode: TruncationModeBytes, Limit: 10000},
		ContextWindow:                  272000,
		MaxContextWindow:               272000,
		EffectiveContextWindowPercent:  95,
		InputModalities:                []string{"text", "image"},
		UsedFallbackModelMetadata:      true,
	}
}

func SupportsServiceTier(info *ModelInfo, serviceTier string) bool {
	if info == nil {
		return false
	}
	serviceTier = strings.TrimSpace(serviceTier)
	if serviceTier == "" {
		return false
	}
	for _, tier := range info.ServiceTiers {
		if tier == serviceTier {
			return true
		}
	}
	return false
}

func ServiceTierForRequest(info *ModelInfo, serviceTier string) string {
	serviceTier = normalizeServiceTierRequestValue(serviceTier)
	if serviceTier == "" || serviceTier == ServiceTierDefaultRequestValue {
		return ""
	}
	if !SupportsServiceTier(info, serviceTier) {
		return ""
	}
	return serviceTier
}

func normalizeServiceTierRequestValue(serviceTier string) string {
	serviceTier = strings.TrimSpace(serviceTier)
	if serviceTier == "fast" {
		return "priority"
	}
	return serviceTier
}

func WithConfigOverrides(model ModelInfo, config *ModelsManagerConfig) ModelInfo {
	if config == nil {
		return model
	}
	if config.ModelSupportsReasoningSummaries != nil && *config.ModelSupportsReasoningSummaries {
		model.SupportsReasoningSummaries = true
	}
	if config.ModelContextWindow > 0 {
		if model.MaxContextWindow > 0 && config.ModelContextWindow > model.MaxContextWindow {
			model.ContextWindow = model.MaxContextWindow
		} else {
			model.ContextWindow = config.ModelContextWindow
		}
	}
	if config.ModelAutoCompactTokenLimit > 0 {
		model.AutoCompactTokenLimit = config.ModelAutoCompactTokenLimit
	}
	if config.ToolOutputTokenLimit > 0 {
		if model.TruncationPolicy.Mode == TruncationModeTokens {
			model.TruncationPolicy.Limit = config.ToolOutputTokenLimit
		} else {
			model.TruncationPolicy.Mode = TruncationModeBytes
			model.TruncationPolicy.Limit = approxBytesForTokens(config.ToolOutputTokenLimit)
		}
	}
	if config.BaseInstructions != "" {
		model.BaseInstructions = config.BaseInstructions
		model.ModelMessages = nil
	} else if !config.PersonalityEnabled {
		model.ModelMessages = nil
	}
	return model
}

func ConstructModelInfoFromCandidates(model string, candidates []ModelInfo, config *ModelsManagerConfig) ModelInfo {
	remote, ok := findModelByLongestPrefix(model, candidates)
	if !ok {
		remote, ok = findModelByNamespacedSuffix(model, candidates)
	}
	var info ModelInfo
	if ok {
		info = remote
		info.Slug = model
		info.UsedFallbackModelMetadata = false
	} else {
		info = ModelInfoFromSlug(model)
	}
	return WithConfigOverrides(info, config)
}

func mergeModelInfos(base []ModelInfo, updates []ModelInfo) []ModelInfo {
	out := cloneModelInfos(base)
	for _, update := range updates {
		replaced := false
		for i := range out {
			if out[i].Slug == update.Slug {
				out[i] = cloneModelInfo(update)
				replaced = true
				break
			}
		}
		if !replaced {
			out = append(out, cloneModelInfo(update))
		}
	}
	return out
}

func defaultModelFromAvailable(available []ModelPreset) string {
	for _, model := range available {
		if model.IsDefault {
			return model.Model
		}
	}
	if len(available) == 0 {
		return ""
	}
	return available[0].Model
}

func requestedModelIsAvailable(requestedModel string, availableModels []ModelPreset) bool {
	if requestedModel == "" {
		return false
	}
	for _, model := range availableModels {
		if model.Model == requestedModel {
			return true
		}
	}
	return false
}

func findModelByLongestPrefix(model string, candidates []ModelInfo) (ModelInfo, bool) {
	var best ModelInfo
	found := false
	for _, candidate := range candidates {
		if !strings.HasPrefix(model, candidate.Slug) {
			continue
		}
		if !found || len(candidate.Slug) > len(best.Slug) {
			best = candidate
			found = true
		}
	}
	return best, found
}

func findModelByNamespacedSuffix(model string, candidates []ModelInfo) (ModelInfo, bool) {
	namespace, suffix, ok := strings.Cut(model, "/")
	if !ok || namespace == "" || strings.Contains(suffix, "/") {
		return ModelInfo{}, false
	}
	for _, r := range namespace {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '_' && r != '-' {
			return ModelInfo{}, false
		}
	}
	return findModelByLongestPrefix(suffix, candidates)
}

func localPersonalityMessagesForSlug(slug string) *ModelMessages {
	switch slug {
	case "gpt-5.2-codex", "exp-codex-personality":
		return &ModelMessages{
			InstructionsTemplate: "You are Codex, a coding agent based on GPT-5.\n\n{{ personality }}\n\n" + BaseInstructions,
			PersonalityDefault:   "",
			PersonalityFriendly:  "You optimize for team morale and being a supportive teammate as much as code quality.",
			PersonalityPragmatic: "You are a deeply pragmatic, effective software engineer.",
		}
	default:
		return nil
	}
}

func bedrockModel(slug string, priority int) ModelInfo {
	return ModelInfo{
		Slug:                           slug,
		DisplayName:                    slug,
		Visibility:                     VisibilityVisible,
		SupportedInAPI:                 true,
		Priority:                       priority,
		BaseInstructions:               BaseInstructions,
		IncludeSkillsUsageInstructions: true,
		TruncationPolicy:               TruncationPolicy{Mode: TruncationModeBytes, Limit: 10000},
		ContextWindow:                  272000,
		MaxContextWindow:               272000,
		EffectiveContextWindowPercent:  95,
		InputModalities:                []string{"text"},
	}
}

func cloneModelInfos(in []ModelInfo) []ModelInfo {
	out := make([]ModelInfo, len(in))
	copy(out, in)
	for i := range out {
		out[i] = cloneModelInfo(out[i])
	}
	return out
}

func cloneModelInfo(in ModelInfo) ModelInfo {
	out := in
	out.SupportedReasoningLevels = cloneStrings(out.SupportedReasoningLevels)
	out.AdditionalSpeedTiers = cloneStrings(out.AdditionalSpeedTiers)
	out.ServiceTiers = cloneStrings(out.ServiceTiers)
	out.InputModalities = cloneStrings(out.InputModalities)
	if out.ModelMessages != nil {
		messages := *out.ModelMessages
		out.ModelMessages = &messages
	}
	return out
}

func cloneStrings(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func approxBytesForTokens(tokens int64) int64 {
	if tokens <= 0 {
		return 0
	}
	return tokens * 4
}
