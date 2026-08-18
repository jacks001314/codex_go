package model

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"codex_go/features"
)

const (
	BaseInstructions = `You are gcode, a coding agent based on GPT-5.

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

	// ModelSpecialtyCyber identifies backend model-catalog specialties for
	// cybersecurity-focused models (Rust MODEL_SPECIALTY_CYBER, f141dc77f0).
	ModelSpecialtyCyber = "cyber"

	ServiceTierDefaultRequestValue = "default"

	ToolModeDirect       = "direct"
	ToolModeCodeMode     = "code_mode"
	ToolModeCodeModeOnly = "code_mode_only"

	VisibilityNone    = "none"
	VisibilityHide    = "hide"
	VisibilityList    = "list"
	VisibilityVisible = "visible"
)

const personalityPlaceholder = "{{ personality }}"

type ModelMessages struct {
	InstructionsTemplate string                     `json:"instructions_template,omitempty"`
	PersonalityDefault   string                     `json:"-"`
	PersonalityFriendly  string                     `json:"-"`
	PersonalityPragmatic string                     `json:"-"`
	CollaborationModes   *CollaborationModeMessages `json:"collaboration_modes,omitempty"`
	MultiAgent           *MultiAgentMessages        `json:"multi_agent,omitempty"`
	TokenBudget          *ModelTokenBudgetConfig    `json:"token_budget,omitempty"`
}

type CollaborationModeMessages struct {
	Default *string `json:"default"`
	Plan    *string `json:"plan"`
}

// MultiAgentMessages mirrors Rust MultiAgentMessages: model-catalog messages
// for multi-agent roles and delegation modes (#38619).
type MultiAgentMessages struct {
	Role *MultiAgentRoleMessages `json:"role,omitempty"`
	Mode *MultiAgentModeMessages `json:"mode,omitempty"`
}

type MultiAgentRoleMessages struct {
	Root     *string `json:"root,omitempty"`
	Subagent *string `json:"subagent,omitempty"`
}

type MultiAgentModeMessages struct {
	Explicit *string `json:"explicit,omitempty"`
	HintText *string `json:"hint_text,omitempty"`
}

// ModelTokenBudgetConfig contains model-owned defaults for the context-window
// token-budget feature.
type ModelTokenBudgetConfig struct {
	ReminderThresholdTokens         int    `json:"reminder_threshold_tokens"`
	ReminderMessageTemplate         string `json:"reminder_message_template"`
	GuidanceMessage                 string `json:"guidance_message"`
	AutoCompactFallbackPrompt       string `json:"auto_compact_fallback_prompt"`
	AutoCompactFallbackBufferTokens int    `json:"auto_compact_fallback_buffer_tokens"`
}

func (m *ModelMessages) UnmarshalJSON(data []byte) error {
	var raw struct {
		InstructionsTemplate  string                     `json:"instructions_template"`
		InstructionsVariables map[string]string          `json:"instructions_variables"`
		CollaborationModes    *CollaborationModeMessages `json:"collaboration_modes"`
		MultiAgent            *MultiAgentMessages        `json:"multi_agent"`
		TokenBudget           *ModelTokenBudgetConfig    `json:"token_budget"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	m.InstructionsTemplate = raw.InstructionsTemplate
	m.CollaborationModes = raw.CollaborationModes
	m.MultiAgent = raw.MultiAgent
	m.TokenBudget = raw.TokenBudget
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
	Slug                           string         `json:"slug"`
	DisplayName                    string         `json:"display_name"`
	Description                    string         `json:"description"`
	DefaultReasoningLevel          string         `json:"default_reasoning_level"`
	SupportedReasoningLevels       []string       `json:"supported_reasoning_levels"`
	Visibility                     string         `json:"visibility"`
	SupportedInAPI                 bool           `json:"supported_in_api"`
	Priority                       int            `json:"priority"`
	AdditionalSpeedTiers           []string       `json:"additional_speed_tiers"`
	ServiceTiers                   []string       `json:"service_tiers"`
	DefaultServiceTier             string         `json:"default_service_tier"`
	BaseInstructions               string         `json:"base_instructions"`
	ModelMessages                  *ModelMessages `json:"model_messages"`
	IncludeSkillsUsageInstructions bool           `json:"include_skills_usage_instructions"`
	IncludePluginUsageInstructions bool           `json:"include_plugin_usage_instructions"`
	IncludeAppsUsageInstructions   bool           `json:"include_apps_usage_instructions"`
	ModelSpecialty                 string         `json:"model_specialty"`
	// SupportsReasoningSummaries mirrors Rust ModelInfo.supports_reasoning_summary_parameter
	// (serde default_true: absent means true). The legacy Go wire name
	// supports_reasoning_summaries is still accepted on parse.
	SupportsReasoningSummaries    bool              `json:"supports_reasoning_summary_parameter"`
	DefaultReasoningSummary       string            `json:"default_reasoning_summary"`
	SupportVerbosity              bool              `json:"support_verbosity"`
	DefaultVerbosity              string            `json:"default_verbosity"`
	WebSearchToolType             string            `json:"web_search_tool_type"`
	TruncationPolicy              TruncationPolicy  `json:"truncation_policy"`
	SupportsParallelToolCalls     bool              `json:"supports_parallel_tool_calls"`
	ToolMode                      string            `json:"tool_mode"`
	MultiAgentVersion             string            `json:"multi_agent_version"`
	SupportsImageDetailOriginal   bool              `json:"supports_image_detail_original"`
	ContextWindow                 int64             `json:"context_window"`
	MaxContextWindow              int64             `json:"max_context_window"`
	AutoCompactTokenLimit         int64             `json:"auto_compact_token_limit"`
	EffectiveContextWindowPercent int               `json:"effective_context_window_percent"`
	InputModalities               []string          `json:"input_modalities"`
	UsedFallbackModelMetadata     bool              `json:"-"`
	SupportsSearchTool            bool              `json:"supports_search_tool"`
	UseResponsesLite              bool              `json:"use_responses_lite"`
	NodeReplAutoReviewRequired    bool              `json:"node_repl_auto_review_required"`
	NodeReplDisabled              bool              `json:"node_repl_disabled"`
	AutoReviewModelOverride       string            `json:"auto_review_model_override"`
	Upgrade                       *ModelInfoUpgrade `json:"upgrade"`
	// AvailabilityNux mirrors Rust ModelInfo.availability_nux (the NUX shown
	// when the model preset becomes accessible to the user).
	AvailabilityNux *ModelAvailabilityNux `json:"availability_nux"`
	// ShellType / ApplyPatchToolType / CompHash / ExperimentalSupportedTools
	// carry the Rust openai_models::ModelInfo wire fields so catalog metadata
	// round-trips identically (L0 model-metadata surface).
	ShellType                  string   `json:"shell_type"`
	ApplyPatchToolType         string   `json:"apply_patch_tool_type"`
	CompHash                   string   `json:"comp_hash"`
	ExperimentalSupportedTools []string `json:"experimental_supported_tools"`
}

// ModelInfoUpgrade carries the replacement model and informational retirement
// time advertised by a model-catalog upgrade entry.
type ModelInfoUpgrade struct {
	Model             string `json:"model"`
	MigrationMarkdown string `json:"migration_markdown"`
	RetirementAt      *int64 `json:"retirement_at,omitempty"`
}

func (u *ModelInfoUpgrade) UnmarshalJSON(data []byte) error {
	var raw struct {
		Model             string          `json:"model"`
		MigrationMarkdown string          `json:"migration_markdown"`
		RetirementAt      json.RawMessage `json:"retirement_at"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*u = ModelInfoUpgrade{
		Model:             raw.Model,
		MigrationMarkdown: raw.MigrationMarkdown,
		RetirementAt:      optionalRFC3339UnixSeconds(raw.RetirementAt),
	}
	return nil
}

func optionalRFC3339UnixSeconds(raw json.RawMessage) *int64 {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || strings.TrimSpace(value) == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil
	}
	unix := parsed.Unix()
	return &unix
}

func (m *ModelInfo) UnmarshalJSON(data []byte) error {
	type rawTruncationPolicy struct {
		Mode  string `json:"mode"`
		Limit int64  `json:"limit"`
	}
	var raw struct {
		Slug                              string                `json:"slug"`
		DisplayName                       string                `json:"display_name"`
		Description                       any                   `json:"description"`
		DefaultReasoningLevel             any                   `json:"default_reasoning_level"`
		SupportedReasoningLevels          []json.RawMessage     `json:"supported_reasoning_levels"`
		Visibility                        string                `json:"visibility"`
		SupportedInAPI                    bool                  `json:"supported_in_api"`
		Priority                          int                   `json:"priority"`
		AdditionalSpeedTiers              []string              `json:"additional_speed_tiers"`
		ServiceTiers                      []json.RawMessage     `json:"service_tiers"`
		DefaultServiceTier                any                   `json:"default_service_tier"`
		BaseInstructions                  string                `json:"base_instructions"`
		ModelMessages                     *ModelMessages        `json:"model_messages"`
		IncludeSkillsUsageInstructions    bool                  `json:"include_skills_usage_instructions"`
		IncludePluginUsageInstructions    bool                  `json:"include_plugin_usage_instructions"`
		IncludeAppsUsageInstructions      *bool                 `json:"include_apps_usage_instructions"`
		ModelSpecialty                    any                   `json:"model_specialty"`
		SupportsReasoningSummaryParameter *bool                 `json:"supports_reasoning_summary_parameter"`
		SupportsReasoningSummariesLegacy  *bool                 `json:"supports_reasoning_summaries"`
		DefaultReasoningSummary           string                `json:"default_reasoning_summary"`
		SupportVerbosity                  bool                  `json:"support_verbosity"`
		DefaultVerbosity                  any                   `json:"default_verbosity"`
		WebSearchToolType                 string                `json:"web_search_tool_type"`
		TruncationPolicy                  rawTruncationPolicy   `json:"truncation_policy"`
		SupportsParallelToolCalls         bool                  `json:"supports_parallel_tool_calls"`
		ToolMode                          any                   `json:"tool_mode"`
		MultiAgentVersion                 any                   `json:"multi_agent_version"`
		SupportsImageDetailOriginal       bool                  `json:"supports_image_detail_original"`
		ContextWindow                     int64                 `json:"context_window"`
		MaxContextWindow                  int64                 `json:"max_context_window"`
		AutoCompactTokenLimit             int64                 `json:"auto_compact_token_limit"`
		EffectiveContextWindowPercent     int                   `json:"effective_context_window_percent"`
		InputModalities                   []string              `json:"input_modalities"`
		SupportsSearchTool                bool                  `json:"supports_search_tool"`
		UseResponsesLite                  bool                  `json:"use_responses_lite"`
		NodeReplAutoReviewRequired        bool                  `json:"node_repl_auto_review_required"`
		NodeReplDisabled                  bool                  `json:"node_repl_disabled"`
		AutoReviewModelOverride           any                   `json:"auto_review_model_override"`
		Upgrade                           *ModelInfoUpgrade     `json:"upgrade"`
		AvailabilityNux                   *ModelAvailabilityNux `json:"availability_nux"`
		ShellType                         string                `json:"shell_type"`
		ApplyPatchToolType                string                `json:"apply_patch_tool_type"`
		CompHash                          string                `json:"comp_hash"`
		ExperimentalSupportedTools        []string              `json:"experimental_supported_tools"`
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
		IncludePluginUsageInstructions: raw.IncludePluginUsageInstructions,
		IncludeAppsUsageInstructions:   defaultTrueBool(raw.IncludeAppsUsageInstructions),
		ModelSpecialty:                 stringFromJSONValue(raw.ModelSpecialty),
		SupportsReasoningSummaries:     reasoningSummariesSupport(raw.SupportsReasoningSummaryParameter, raw.SupportsReasoningSummariesLegacy),
		DefaultReasoningSummary:        raw.DefaultReasoningSummary,
		SupportVerbosity:               raw.SupportVerbosity,
		DefaultVerbosity:               stringFromJSONValue(raw.DefaultVerbosity),
		WebSearchToolType:              raw.WebSearchToolType,
		TruncationPolicy:               TruncationPolicy(raw.TruncationPolicy),
		SupportsParallelToolCalls:      raw.SupportsParallelToolCalls,
		ToolMode:                       knownToolMode(stringFromJSONValue(raw.ToolMode)),
		MultiAgentVersion:              knownMultiAgentVersion(stringFromJSONValue(raw.MultiAgentVersion)),
		SupportsImageDetailOriginal:    raw.SupportsImageDetailOriginal,
		ContextWindow:                  raw.ContextWindow,
		MaxContextWindow:               raw.MaxContextWindow,
		AutoCompactTokenLimit:          raw.AutoCompactTokenLimit,
		EffectiveContextWindowPercent:  raw.EffectiveContextWindowPercent,
		InputModalities:                cloneStrings(raw.InputModalities),
		SupportsSearchTool:             raw.SupportsSearchTool,
		UseResponsesLite:               raw.UseResponsesLite,
		NodeReplAutoReviewRequired:     raw.NodeReplAutoReviewRequired,
		NodeReplDisabled:               raw.NodeReplDisabled,
		AutoReviewModelOverride:        stringFromJSONValue(raw.AutoReviewModelOverride),
		Upgrade:                        raw.Upgrade,
		AvailabilityNux:                raw.AvailabilityNux,
		ShellType:                      raw.ShellType,
		ApplyPatchToolType:             raw.ApplyPatchToolType,
		CompHash:                       raw.CompHash,
		ExperimentalSupportedTools:     cloneStrings(raw.ExperimentalSupportedTools),
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
	MultiAgentVersion        string
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

// LoadModelsResponseFromFile mirrors Rust's load_catalog_json for the
// model_catalog_json config: the file must parse as a ModelsResponse and
// contain at least one model.
func LoadModelsResponseFromFile(path string) (ModelsResponse, error) {
	data, err := os.ReadFile(strings.TrimSpace(path))
	if err != nil {
		return ModelsResponse{}, err
	}
	var catalog ModelsResponse
	if err := json.Unmarshal(data, &catalog); err != nil {
		return ModelsResponse{}, fmt.Errorf("failed to parse model_catalog_json path %q as JSON: %w", strings.TrimSpace(path), err)
	}
	if len(catalog.Models) == 0 {
		return ModelsResponse{}, fmt.Errorf("model_catalog_json path %q must contain at least one model", strings.TrimSpace(path))
	}
	return catalog, nil
}

// ModelsCatalogFromConfigValues mirrors Rust's model_catalog_json config: an
// optional path to a JSON model catalog applied at config read time. Invalid
// or empty catalogs fall back to the bundled catalog (Rust surfaces a config
// load error instead; callers that prefer strictness can use
// LoadModelsResponseFromFile directly).
func ModelsCatalogFromConfigValues(values map[string]any) *ModelsResponse {
	if values == nil {
		return nil
	}
	raw, ok := values["model_catalog_json"]
	if !ok {
		return nil
	}
	path := strings.TrimSpace(fmt.Sprint(raw))
	if path == "" {
		return nil
	}
	catalog, err := LoadModelsResponseFromFile(path)
	if err != nil || len(catalog.Models) == 0 {
		return nil
	}
	return &catalog
}

func fallbackBundledModelsResponse() ModelsResponse {
	return ModelsResponse{
		Models: []ModelInfo{
			{
				Slug: "gpt-5.6-sol", DisplayName: "GPT-5.6-Sol", Description: "Latest frontier agentic coding model.",
				Visibility: VisibilityList, SupportedInAPI: true, Priority: 1, BaseInstructions: BaseInstructions,
				ToolMode: ToolModeCodeModeOnly, MultiAgentVersion: "v2", DefaultReasoningLevel: "low",
				SupportedReasoningLevels: []string{"low", "medium", "high", "xhigh", "max", "ultra"},
				ServiceTiers:             []string{"priority"},
				SupportVerbosity:         true, DefaultVerbosity: "low", WebSearchToolType: "text_and_image",
				TruncationPolicy: TruncationPolicy{Mode: TruncationModeTokens, Limit: 10000}, SupportsImageDetailOriginal: true,
				UseResponsesLite: true, DefaultReasoningSummary: "none",
				ContextWindow: 272000, MaxContextWindow: 872000, EffectiveContextWindowPercent: 95,
				InputModalities: []string{"text", "image"}, SupportsParallelToolCalls: true,
			},
			{
				Slug: "gpt-5.6-terra", DisplayName: "GPT-5.6-Terra", Description: "Balanced agentic coding model for everyday work.",
				Visibility: VisibilityList, SupportedInAPI: true, Priority: 2, BaseInstructions: BaseInstructions,
				ToolMode: ToolModeCodeModeOnly, MultiAgentVersion: "v2", DefaultReasoningLevel: "medium",
				SupportedReasoningLevels: []string{"low", "medium", "high", "xhigh", "max", "ultra"},
				ServiceTiers:             []string{"priority"},
				SupportVerbosity:         true, DefaultVerbosity: "low", WebSearchToolType: "text_and_image",
				TruncationPolicy: TruncationPolicy{Mode: TruncationModeTokens, Limit: 10000}, SupportsImageDetailOriginal: true,
				UseResponsesLite: true, DefaultReasoningSummary: "none",
				ContextWindow: 272000, MaxContextWindow: 872000, EffectiveContextWindowPercent: 95,
				InputModalities: []string{"text", "image"}, SupportsParallelToolCalls: true,
			},
			{
				Slug: "gpt-5.6-luna", DisplayName: "GPT-5.6-Luna", Description: "Fast and affordable agentic coding model.",
				Visibility: VisibilityList, SupportedInAPI: true, Priority: 3, BaseInstructions: BaseInstructions,
				ToolMode: ToolModeCodeModeOnly, MultiAgentVersion: "v1", DefaultReasoningLevel: "medium",
				SupportedReasoningLevels: []string{"low", "medium", "high", "xhigh", "max"},
				ServiceTiers:             []string{"priority"},
				SupportVerbosity:         true, DefaultVerbosity: "low", WebSearchToolType: "text_and_image",
				TruncationPolicy: TruncationPolicy{Mode: TruncationModeTokens, Limit: 10000}, SupportsImageDetailOriginal: true,
				UseResponsesLite: true, DefaultReasoningSummary: "none",
				ContextWindow: 272000, MaxContextWindow: 872000, EffectiveContextWindowPercent: 95,
				InputModalities: []string{"text", "image"}, SupportsParallelToolCalls: true,
			},
			{
				Slug:                           "gpt-5.5",
				DisplayName:                    "GPT-5.5",
				Description:                    "Frontier model for complex coding, research, and real-world work.",
				Visibility:                     VisibilityList,
				SupportedInAPI:                 true,
				Priority:                       7,
				ServiceTiers:                   []string{"priority"},
				BaseInstructions:               BaseInstructions,
				IncludeSkillsUsageInstructions: true,
				DefaultReasoningLevel:          "medium",
				SupportedReasoningLevels:       []string{"low", "medium", "high", "xhigh"},
				TruncationPolicy:               TruncationPolicy{Mode: TruncationModeBytes, Limit: 10000},
				ContextWindow:                  272000,
				MaxContextWindow:               272000,
				EffectiveContextWindowPercent:  95,
				InputModalities:                []string{"text", "image"},
				SupportsParallelToolCalls:      true,
			},
			{
				Slug: "gpt-5.2", DisplayName: "GPT-5.2", Description: "Optimized for professional work and long-running agents.",
				Visibility: VisibilityList, SupportedInAPI: true, Priority: 29, BaseInstructions: BaseInstructions,
				DefaultReasoningLevel: "medium", SupportedReasoningLevels: []string{"low", "medium", "high", "xhigh"},
				ContextWindow: 272000, MaxContextWindow: 272000, EffectiveContextWindowPercent: 95,
				InputModalities: []string{"text", "image"}, SupportsParallelToolCalls: true,
			},
			{
				Slug:                           "gpt-5.4",
				DisplayName:                    "GPT-5.4",
				Description:                    "Strong model for everyday coding.",
				Visibility:                     VisibilityHide,
				SupportedInAPI:                 true,
				Priority:                       16,
				BaseInstructions:               BaseInstructions,
				IncludeSkillsUsageInstructions: true,
				DefaultReasoningLevel:          "medium",
				SupportedReasoningLevels:       []string{"low", "medium", "high", "xhigh"},
				ServiceTiers:                   []string{"priority"},
				TruncationPolicy:               TruncationPolicy{Mode: TruncationModeBytes, Limit: 10000},
				ContextWindow:                  272000,
				MaxContextWindow:               1000000,
				EffectiveContextWindowPercent:  95,
				InputModalities:                []string{"text", "image"},
				SupportsParallelToolCalls:      true,
			},
			{
				Slug:                           "gpt-5.4-mini",
				DisplayName:                    "GPT-5.4-Mini",
				Description:                    "Small, fast, and cost-efficient model for simpler coding tasks.",
				Visibility:                     VisibilityHide,
				SupportedInAPI:                 true,
				Priority:                       23,
				BaseInstructions:               BaseInstructions,
				IncludeSkillsUsageInstructions: true,
				DefaultReasoningLevel:          "medium",
				SupportedReasoningLevels:       []string{"low", "medium", "high", "xhigh"},
				TruncationPolicy:               TruncationPolicy{Mode: TruncationModeBytes, Limit: 10000},
				ContextWindow:                  272000,
				MaxContextWindow:               272000,
				EffectiveContextWindowPercent:  95,
				InputModalities:                []string{"text", "image"},
				SupportsParallelToolCalls:      true,
			},
			{
				Slug:                          "codex-auto-review",
				DisplayName:                   "Codex Auto Review",
				Description:                   "Automatic approval review model for Codex.",
				Visibility:                    VisibilityHide,
				SupportedInAPI:                true,
				Priority:                      43,
				BaseInstructions:              BaseInstructions,
				DefaultReasoningLevel:         "medium",
				SupportedReasoningLevels:      []string{"low", "medium", "high", "xhigh"},
				TruncationPolicy:              TruncationPolicy{Mode: TruncationModeBytes, Limit: 10000},
				ContextWindow:                 272000,
				MaxContextWindow:              1000000,
				EffectiveContextWindowPercent: 95,
				InputModalities:               []string{"text", "image"},
				SupportsParallelToolCalls:     true,
			},
		},
	}
}

func AmazonBedrockModelCatalog() ModelsResponse {
	return ModelsResponse{
		Models: []ModelInfo{
			bedrockModel(AmazonBedrockGPT55ModelID, 0),
			bedrockModel(AmazonBedrockGPT54ModelID, 10),
			bedrockModelWithMaxContextWindow(AmazonBedrockGPT56SolModelID, 20, 872000),
			bedrockModelWithMaxContextWindow(AmazonBedrockGPT56TerraModelID, 30, 872000),
			bedrockModelWithMaxContextWindow(AmazonBedrockGPT56LunaModelID, 40, 872000),
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
			MultiAgentVersion:        model.MultiAgentVersion,
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

// defaultTrueBool returns the explicit value or true when absent, mirroring
// Rust's `#[serde(default = "default_true")]` for include_apps_usage_instructions.
func defaultTrueBool(value *bool) bool {
	if value == nil {
		return true
	}
	return *value
}

// reasoningSummariesSupport resolves the model's reasoning-summary support,
// mirroring Rust ModelInfo.supports_reasoning_summary_parameter with
// #[serde(default = "default_true")]: the Rust wire name wins, the legacy Go
// name is a parse-time alias, and an absent field defaults to true.
func reasoningSummariesSupport(primary, legacy *bool) bool {
	if primary != nil {
		return *primary
	}
	if legacy != nil {
		return *legacy
	}
	return true
}

func knownMultiAgentVersion(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "disabled", "v1", "v2":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func knownToolMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case ToolModeDirect, ToolModeCodeMode, ToolModeCodeModeOnly:
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

// ResolveToolMode mirrors Rust's requested_tool_mode in
// codex-rs/core/src/tools/mod.rs: an explicit model tool_mode wins; an unset
// tool_mode falls back to the code_mode_only/code_mode feature flags, and
// defaults to direct mode when neither is enabled. Custom providers (for
// example DeepSeek) that reject the code-mode exec freeform tool therefore get
// a direct tool surface unless code mode is explicitly requested.
func ResolveToolMode(modelToolMode string, featureSettings map[string]bool) string {
	if mode := knownToolMode(modelToolMode); mode != "" {
		return mode
	}
	switch {
	case features.Enabled(featureSettings, "code_mode_only"):
		return ToolModeCodeModeOnly
	case features.Enabled(featureSettings, "code_mode"):
		return ToolModeCodeMode
	default:
		return ToolModeDirect
	}
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
		IncludeAppsUsageInstructions:   false,
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
		setInstructionsTemplate(&model, config.BaseInstructions)
	} else if !config.PersonalityEnabled {
		usesLocalPersonalityTemplate := model.UsedFallbackModelMetadata &&
			(model.Slug == "gpt-5.2-codex" || model.Slug == "exp-codex-personality")
		if usesLocalPersonalityTemplate {
			setInstructionsTemplate(&model, BaseInstructions)
		} else if messages := model.ModelMessages; messages != nil && strings.TrimSpace(messages.InstructionsTemplate) != "" {
			personalityDefault, _ := messages.PersonalityMessage("")
			setInstructionsTemplate(&model, strings.ReplaceAll(messages.InstructionsTemplate, personalityPlaceholder, personalityDefault))
		} else {
			clearInstructionVariables(&model)
		}
	}
	return model
}

// setInstructionsTemplate mirrors Rust's model_messages.instructions_template
// override: the template becomes the sole instruction source and any
// personality variables are cleared while the remaining message fields
// (approvals, collaboration modes, token budget, ...) are preserved
// (Rust df72fdb415).
func setInstructionsTemplate(model *ModelInfo, template string) {
	if model == nil || model.ModelMessages == nil {
		if model == nil {
			return
		}
		model.ModelMessages = &ModelMessages{}
	}
	messages := model.ModelMessages
	messages.InstructionsTemplate = template
	messages.PersonalityDefault = ""
	messages.PersonalityFriendly = ""
	messages.PersonalityPragmatic = ""
	// Rust #38619: a base-instructions override replaces the message set, so
	// catalog-provided multi-agent role/mode messages are cleared too.
	messages.MultiAgent = nil
}

// clearInstructionVariables removes the personality instruction source while
// preserving non-instruction message fields.
func clearInstructionVariables(model *ModelInfo) {
	if model == nil || model.ModelMessages == nil {
		return
	}
	messages := model.ModelMessages
	messages.InstructionsTemplate = ""
	messages.PersonalityDefault = ""
	messages.PersonalityFriendly = ""
	messages.PersonalityPragmatic = ""
	if messages.CollaborationModes == nil && messages.TokenBudget == nil {
		model.ModelMessages = nil
	}
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
			InstructionsTemplate: "You are gcode, a coding agent based on GPT-5.\n\n{{ personality }}\n\n" + BaseInstructions,
			PersonalityDefault:   "",
			PersonalityFriendly:  "You optimize for team morale and being a supportive teammate as much as code quality.",
			PersonalityPragmatic: "You are a deeply pragmatic, effective software engineer.",
		}
	default:
		return nil
	}
}

func bedrockModel(slug string, priority int) ModelInfo {
	return bedrockModelWithMaxContextWindow(slug, priority, 272000)
}

// bedrockModelWithMaxContextWindow mirrors Rust #39102: Amazon Bedrock GPT-5.6
// variants allow context-window overrides up to 872,000 tokens while GPT-5.5 and
// GPT-5.4 keep the shared 272,000-token Bedrock window.
func bedrockModelWithMaxContextWindow(slug string, priority int, maxContextWindow int64) ModelInfo {
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
		MaxContextWindow:               maxContextWindow,
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
		if messages.CollaborationModes != nil {
			collaborationModes := *messages.CollaborationModes
			collaborationModes.Default = cloneStringPointer(collaborationModes.Default)
			collaborationModes.Plan = cloneStringPointer(collaborationModes.Plan)
			messages.CollaborationModes = &collaborationModes
		}
		if messages.TokenBudget != nil {
			tokenBudget := *messages.TokenBudget
			messages.TokenBudget = &tokenBudget
		}
		out.ModelMessages = &messages
	}
	out.Upgrade = cloneModelInfoUpgrade(out.Upgrade)
	return out
}

func cloneModelInfoUpgrade(in *ModelInfoUpgrade) *ModelInfoUpgrade {
	if in == nil {
		return nil
	}
	out := *in
	if in.RetirementAt != nil {
		value := *in.RetirementAt
		out.RetirementAt = &value
	}
	return &out
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
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
