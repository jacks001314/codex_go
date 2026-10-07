package model

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestStaticModelsManagerListsSortedModelsAndMarksDefault(t *testing.T) {
	manager := NewStaticModelsManager(ModelsResponse{Models: []ModelInfo{
		{Slug: "slow", DisplayName: "slow", Visibility: VisibilityVisible, SupportedInAPI: true, Priority: 20},
		{Slug: "fast", DisplayName: "fast", Visibility: VisibilityVisible, SupportedInAPI: true, Priority: 5},
		{Slug: "hidden", DisplayName: "hidden", Visibility: VisibilityNone, SupportedInAPI: true, Priority: 0},
	}})

	models := manager.ListModels(RefreshOffline)
	if len(models) != 2 {
		t.Fatalf("models len = %d", len(models))
	}
	if models[0].Model != "fast" || !models[0].IsDefault {
		t.Fatalf("first model = %#v", models[0])
	}
	if models[1].Model != "slow" || models[1].IsDefault {
		t.Fatalf("second model = %#v", models[1])
	}
}

// TestModelInfoReasoningSummariesParseMirrorsRustDefaultTrue pins the
// supports_reasoning_summary_parameter parse semantics: absent defaults true
// (Rust serde default_true), the Rust wire name wins, and the legacy Go name
// remains a parse-time alias.
func TestModelInfoReasoningSummariesParseMirrorsRustDefaultTrue(t *testing.T) {
	base := `{"models":[{"slug":"gpt-test","display_name":"GPT Test"}]}`
	var absent ModelsResponse
	if err := json.Unmarshal([]byte(base), &absent); err != nil {
		t.Fatalf("unmarshal absent: %v", err)
	}
	if !absent.Models[0].SupportsReasoningSummaries {
		t.Fatal("absent supports_reasoning_summary_parameter should default true like Rust")
	}

	var rustFalse ModelsResponse
	if err := json.Unmarshal([]byte(`{"models":[{"slug":"gpt-test","display_name":"GPT Test","supports_reasoning_summary_parameter":false}]}`), &rustFalse); err != nil {
		t.Fatalf("unmarshal rust-false: %v", err)
	}
	if rustFalse.Models[0].SupportsReasoningSummaries {
		t.Fatal("supports_reasoning_summary_parameter=false should parse as false")
	}

	var legacy ModelsResponse
	if err := json.Unmarshal([]byte(`{"models":[{"slug":"gpt-test","display_name":"GPT Test","supports_reasoning_summaries":false}]}`), &legacy); err != nil {
		t.Fatalf("unmarshal legacy: %v", err)
	}
	if legacy.Models[0].SupportsReasoningSummaries {
		t.Fatal("legacy supports_reasoning_summaries=false should parse as false")
	}
}

func TestModelCatalogPreservesKnownMultiAgentVersion(t *testing.T) {
	var catalog ModelsResponse
	err := json.Unmarshal([]byte(`{"models":[
		{"slug":"v2","display_name":"V2","visibility":"list","supported_in_api":true,"multi_agent_version":"v2"},
		{"slug":"future","display_name":"Future","visibility":"list","supported_in_api":true,"multi_agent_version":"v99"}
	]}`), &catalog)
	if err != nil {
		t.Fatal(err)
	}
	models := BuildAvailableModels(catalog.Models)
	if len(models) != 2 || models[0].MultiAgentVersion != "v2" || models[1].MultiAgentVersion != "" {
		t.Fatalf("multi-agent versions = %#v", models)
	}
}

func TestModelCatalogPreservesKnownToolMode(t *testing.T) {
	var catalog ModelsResponse
	err := json.Unmarshal([]byte(`{"models":[
		{"slug":"code","display_name":"Code","visibility":"list","supported_in_api":true,"tool_mode":"code_mode_only"},
		{"slug":"future","display_name":"Future","visibility":"list","supported_in_api":true,"tool_mode":"future_mode"}
	]}`), &catalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Models) != 2 || catalog.Models[0].ToolMode != ToolModeCodeModeOnly || catalog.Models[1].ToolMode != "" {
		t.Fatalf("tool modes = %#v", catalog.Models)
	}
}

func TestResolveToolModeMirrorsRustRequestedToolMode(t *testing.T) {
	for _, testCase := range []struct {
		name            string
		modelToolMode   string
		featureSettings map[string]bool
		want            string
	}{
		{name: "explicit-direct-wins", modelToolMode: ToolModeDirect, featureSettings: map[string]bool{"code_mode": true}, want: ToolModeDirect},
		{name: "explicit-code-mode-wins", modelToolMode: ToolModeCodeMode, featureSettings: map[string]bool{"code_mode_only": true}, want: ToolModeCodeMode},
		{name: "explicit-code-mode-only-wins", modelToolMode: ToolModeCodeModeOnly, want: ToolModeCodeModeOnly},
		{name: "unknown-treats-as-unset", modelToolMode: "future_mode", want: ToolModeDirect},
		{name: "unset-defaults-to-direct", want: ToolModeDirect},
		{name: "code-mode-feature", featureSettings: map[string]bool{"code_mode": true}, want: ToolModeCodeMode},
		{name: "code-mode-only-feature", featureSettings: map[string]bool{"code_mode_only": true}, want: ToolModeCodeModeOnly},
		{name: "code-mode-only-beats-code-mode", featureSettings: map[string]bool{"code_mode": true, "code_mode_only": true}, want: ToolModeCodeModeOnly},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := ResolveToolMode(testCase.modelToolMode, testCase.featureSettings); got != testCase.want {
				t.Fatalf("ResolveToolMode(%q, %#v) = %q, want %q", testCase.modelToolMode, testCase.featureSettings, got, testCase.want)
			}
		})
	}
}

func TestBuildAvailableModelsMarksFirstPickerVisibleDefault(t *testing.T) {
	models := BuildAvailableModels([]ModelInfo{
		{Slug: "hidden", DisplayName: "Hidden", Visibility: VisibilityHide, SupportedInAPI: true, Priority: 0},
		{Slug: "listed", DisplayName: "Listed", Visibility: VisibilityList, SupportedInAPI: true, Priority: 1},
	})
	if len(models) != 2 {
		t.Fatalf("models len = %d", len(models))
	}
	if models[0].IsDefault || !models[1].IsDefault {
		t.Fatalf("defaults = %#v", models)
	}
}

func TestBuildAvailableModelsPreservesReasoningFields(t *testing.T) {
	models := BuildAvailableModels([]ModelInfo{
		{
			Slug:                     "gpt-reasoning",
			DisplayName:              "GPT Reasoning",
			Visibility:               VisibilityList,
			SupportedInAPI:           true,
			DefaultReasoningLevel:    "medium",
			SupportedReasoningLevels: []string{"low", "medium", "high"},
		},
	})
	if len(models) != 1 {
		t.Fatalf("models len = %d", len(models))
	}
	got := models[0]
	if got.DefaultReasoningLevel != "medium" {
		t.Fatalf("default reasoning = %q", got.DefaultReasoningLevel)
	}
	if len(got.SupportedReasoningLevels) != 3 || got.SupportedReasoningLevels[2] != "high" {
		t.Fatalf("supported reasoning = %#v", got.SupportedReasoningLevels)
	}
}

func TestFallbackBundledModelsMatchCurrentRustDefault(t *testing.T) {
	manager := NewStaticModelsManager(fallbackBundledModelsResponse())
	if got := manager.GetDefaultModel("", true, RefreshOffline); got != "gpt-6.1-sol" {
		t.Fatalf("default model = %q", got)
	}
	info := manager.GetModelInfo("gpt-6.1-sol", nil)
	if info.DefaultReasoningLevel != "low" || info.ContextWindow != 272000 || info.ToolMode != ToolModeCodeModeOnly || !info.UseResponsesLite {
		t.Fatalf("default model info = %#v", info)
	}
}

// TestFallbackBundledCatalogCarriesGPT6FamilyLikeRust pins the GPT-6 family
// Rust ships in models-manager/models.json (@b17c74cfd5) as the Go fallback
// bundled catalog's leading entries: four slugs, multi-agent V2, the bundled
// long-context windows, and the global priority order that makes gpt-6.1-sol
// the offline default (Rust sorts by priority and marks the first
// picker-visible preset as default).
func TestFallbackBundledCatalogCarriesGPT6FamilyLikeRust(t *testing.T) {
	manager := NewStaticModelsManager(fallbackBundledModelsResponse())

	wantPriority := map[string]int{
		"gpt-6.1-sol":   1,
		"gpt-6-astra":   2,
		"gpt-6-sol":     3,
		"gpt-6-luna":    4,
		"gpt-5.6-sol":   5,
		"gpt-5.6-terra": 8,
		"gpt-5.6-luna":  9,
	}
	for slug, priority := range wantPriority {
		info := manager.GetModelInfo(slug, nil)
		if info.Slug != slug {
			t.Fatalf("GetModelInfo(%q).Slug = %q", slug, info.Slug)
		}
		if info.Priority != priority {
			t.Fatalf("%s Priority = %d, want %d", slug, info.Priority, priority)
		}
	}

	// Every GPT-6 entry is picker-visible, multi-agent V2, code-mode only, and
	// keeps the bundled long-context window.
	for _, slug := range []string{"gpt-6.1-sol", "gpt-6-astra", "gpt-6-sol", "gpt-6-luna"} {
		info := manager.GetModelInfo(slug, nil)
		if info.Visibility != VisibilityList || !info.SupportedInAPI {
			t.Fatalf("%s visibility/api = %q/%v, want list/true", slug, info.Visibility, info.SupportedInAPI)
		}
		if info.MultiAgentVersion != "v2" {
			t.Fatalf("%s MultiAgentVersion = %q, want v2", slug, info.MultiAgentVersion)
		}
		if info.ToolMode != ToolModeCodeModeOnly {
			t.Fatalf("%s ToolMode = %q, want %q", slug, info.ToolMode, ToolModeCodeModeOnly)
		}
		if info.ContextWindow != 272000 || info.MaxContextWindow != 872000 {
			t.Fatalf("%s context = %d/%d, want 272000/872000", slug, info.ContextWindow, info.MaxContextWindow)
		}
		if !info.UseResponsesLite {
			t.Fatalf("%s UseResponsesLite = false, want true", slug)
		}
	}

	// The GPT-6 family leads the picker list in priority order and gpt-6.1-sol
	// is the offline default.
	presets := manager.ListModels(RefreshOffline)
	var leading []string
	for _, preset := range presets {
		if modelVisibleInPicker(preset.Visibility) {
			leading = append(leading, preset.Model)
		}
	}
	wantLeading := []string{
		"gpt-6.1-sol", "gpt-6-astra", "gpt-6-sol", "gpt-6-luna",
		"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna",
	}
	if len(leading) < len(wantLeading) {
		t.Fatalf("picker models = %#v, want at least %#v", leading, wantLeading)
	}
	for i, want := range wantLeading {
		if leading[i] != want {
			t.Fatalf("picker order[%d] = %q, want %q (all %#v)", i, leading[i], want, leading)
		}
	}
	if len(presets) == 0 || !presets[0].IsDefault || presets[0].Model != "gpt-6.1-sol" {
		t.Fatalf("default preset = %#v, want gpt-6.1-sol", presets)
	}
	if got := manager.GetDefaultModel("", true, RefreshOffline); got != "gpt-6.1-sol" {
		t.Fatalf("GetDefaultModel = %q, want gpt-6.1-sol", got)
	}
}

func TestFallbackBundledCatalogDropsGPT54LikeRust(t *testing.T) {
	// Rust #47932 (694d8d45bd): gpt-5.4 is removed from the bundled catalog.
	// The GPT-5.4 Mini entry stays because its migration target (GPT-6 Luna)
	// still needs the saved-selection prompt.
	presets := NewStaticModelsManager(fallbackBundledModelsResponse()).ListModels(RefreshOffline)
	sawMini := false
	for _, preset := range presets {
		if preset.Model == "gpt-5.4" {
			t.Fatalf("bundled catalog still lists %q", preset.Model)
		}
		if preset.Model == "gpt-5.4-mini" {
			sawMini = true
		}
	}
	if !sawMini {
		t.Fatal("bundled catalog lost the gpt-5.4-mini entry")
	}
}

func TestBundledGPT56ModelsAllowLongContextOverrideLikeRust(t *testing.T) {
	// Rust #39102 raises the GPT-5.6 maximum context window to 872,000 tokens
	// (models-manager/models.json max_context_window 272000 -> 872000).
	for _, slug := range []string{"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna"} {
		info := NewStaticModelsManager(fallbackBundledModelsResponse()).GetModelInfo(slug, nil)
		if info.MaxContextWindow != 872000 {
			t.Fatalf("%s MaxContextWindow = %d, want 872000", slug, info.MaxContextWindow)
		}
		if info.ContextWindow != 272000 {
			t.Fatalf("%s ContextWindow = %d, want 272000", slug, info.ContextWindow)
		}

		// A 1,000,000-token override clamps to the new 872,000 maximum
		// (mirror manager_tests.rs get_model_info_applies_long_context_override...).
		overridden := WithConfigOverrides(info, &ModelsManagerConfig{ModelContextWindow: 1_000_000})
		if overridden.ContextWindow != 872000 {
			t.Fatalf("%s overridden ContextWindow = %d, want 872000", slug, overridden.ContextWindow)
		}
	}
}

func TestBedrockGPT56ModelsUseLongContextWindowLikeRust(t *testing.T) {
	// Rust #39102 rebuilds the Amazon Bedrock GPT-5.6 entries with a
	// max_context_window of 872,000 while GPT-5.5/GPT-5.4 keep 272,000.
	catalog := AmazonBedrockModelCatalog()
	for _, model := range catalog.Models {
		want := int64(272000)
		switch model.Slug {
		case AmazonBedrockGPT61SolModelID, AmazonBedrockGPT6AstraModelID, AmazonBedrockGPT6SolModelID,
			AmazonBedrockGPT6LunaModelID, AmazonBedrockGPT56SolModelID, AmazonBedrockGPT56TerraModelID,
			AmazonBedrockGPT56LunaModelID:
			want = 872000
		}
		if model.MaxContextWindow != want {
			t.Fatalf("%s MaxContextWindow = %d, want %d", model.Slug, model.MaxContextWindow, want)
		}
	}
}

func TestLoadModelsResponseFromFileMirrorsRustModelCatalogJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	if err := os.WriteFile(path, []byte(`{"models":[{"slug":"deepseek-v4-flash","context_window":1048576,"max_context_window":1048576,"effective_context_window_percent":95}]}`), 0o600); err != nil {
		t.Fatalf("write catalog: %v", err)
	}
	catalog, err := LoadModelsResponseFromFile(path)
	if err != nil {
		t.Fatalf("LoadModelsResponseFromFile() error = %v", err)
	}
	if len(catalog.Models) != 1 || catalog.Models[0].Slug != "deepseek-v4-flash" || catalog.Models[0].ContextWindow != 1048576 {
		t.Fatalf("catalog = %#v", catalog.Models)
	}
	info := NewStaticModelsManager(catalog).GetModelInfo("deepseek-v4-flash", nil)
	if info.Slug != "deepseek-v4-flash" || info.ContextWindow != 1048576 || info.UsedFallbackModelMetadata {
		t.Fatalf("custom model info = %#v", info)
	}
}

func TestLoadModelsResponseFromFileRejectsEmptyOrInvalidLikeRust(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(empty, []byte(`{"models":[]}`), 0o600); err != nil {
		t.Fatalf("write catalog: %v", err)
	}
	if _, err := LoadModelsResponseFromFile(empty); err == nil || !strings.Contains(err.Error(), "must contain at least one model") {
		t.Fatalf("empty catalog error = %v", err)
	}
	invalid := filepath.Join(dir, "invalid.json")
	if err := os.WriteFile(invalid, []byte(`{not json`), 0o600); err != nil {
		t.Fatalf("write catalog: %v", err)
	}
	if _, err := LoadModelsResponseFromFile(invalid); err == nil || !strings.Contains(err.Error(), "failed to parse model_catalog_json") {
		t.Fatalf("invalid catalog error = %v", err)
	}
}

func TestModelsCatalogFromConfigValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	if err := os.WriteFile(path, []byte(`{"models":[{"slug":"deepseek-v4-pro","context_window":1048576}]}`), 0o600); err != nil {
		t.Fatalf("write catalog: %v", err)
	}
	if catalog := ModelsCatalogFromConfigValues(map[string]any{"model_catalog_json": path}); catalog == nil || len(catalog.Models) != 1 {
		t.Fatalf("catalog = %#v", catalog)
	}
	if catalog := ModelsCatalogFromConfigValues(nil); catalog != nil {
		t.Fatalf("nil values catalog = %#v", catalog)
	}
	if catalog := ModelsCatalogFromConfigValues(map[string]any{"model_catalog_json": filepath.Join(t.TempDir(), "missing.json")}); catalog != nil {
		t.Fatalf("missing catalog = %#v", catalog)
	}
}

func TestDefaultBaseInstructionsRequirePreambleBeforeTools(t *testing.T) {
	// The fallback base instructions are Rust's models-manager/prompt.md, so the
	// asserted guidance matches that document.
	for _, want := range []string{"Before making tool calls, send a brief preamble", "### Preamble messages", "immediately about to be done next", "# Tool Guidelines", "# AGENTS.md spec"} {
		if !strings.Contains(BaseInstructions, want) {
			t.Fatalf("BaseInstructions missing %q", want)
		}
	}
}

func TestModelInfoUnmarshalRustCatalogShape(t *testing.T) {
	var catalog ModelsResponse
	if err := json.Unmarshal([]byte(`{
		"models": [{
			"slug": "gpt-test",
			"display_name": "GPT Test",
			"description": null,
			"default_reasoning_level": "medium",
			"supported_reasoning_levels": [{"effort": "low", "description": "Low"}],
			"visibility": "list",
			"supported_in_api": true,
			"priority": 1,
			"service_tiers": [{"id": "priority", "name": "Fast", "description": "Fast tier"}],
			"additional_speed_tiers": ["fast"],
			"default_service_tier": "priority",
			"base_instructions": "base",
			"model_messages": {
				"instructions_template": "Hello {{ personality }}",
				"instructions_variables": {
					"personality_default": "Default",
					"personality_friendly": "Friendly",
					"personality_pragmatic": "Pragmatic"
				},
				"collaboration_modes": {
					"default": ""
				},
				"token_budget": {
					"reminder_threshold_tokens": 12000,
					"reminder_message_template": "remaining: {n_remaining}",
					"guidance_message": "keep notes",
					"auto_compact_fallback_prompt": "summarize",
					"auto_compact_fallback_buffer_tokens": 8000
				}
			},
			"truncation_policy": {"mode": "tokens", "limit": 10000},
			"supports_parallel_tool_calls": true,
			"tool_mode": "code_mode_only",
			"context_window": 272000,
			"max_context_window": 1000000,
			"effective_context_window_percent": 95,
			"input_modalities": ["text", "image"],
			"supports_search_tool": true,
			"shell_type": "unified_exec",
			"apply_patch_tool_type": "freeform",
			"comp_hash": "abc123",
			"experimental_supported_tools": ["web_search"],
			"availability_nux": {"message": "Welcome to GPT Test"},
			"node_repl_auto_review_required": true,
			"node_repl_disabled": true
		}]
	}`), &catalog); err != nil {
		t.Fatalf("Unmarshal catalog returned error: %v", err)
	}
	model := catalog.Models[0]
	if model.Description != "" || model.SupportedReasoningLevels[0] != "low" || model.ServiceTiers[0] != "priority" {
		t.Fatalf("model = %#v", model)
	}
	if model.Visibility != VisibilityList || !model.SupportsParallelToolCalls || !model.SupportsSearchTool || model.ToolMode != ToolModeCodeModeOnly {
		t.Fatalf("model flags = %#v", model)
	}
	if model.ShellType != "unified_exec" || model.ApplyPatchToolType != "freeform" || model.CompHash != "abc123" ||
		len(model.ExperimentalSupportedTools) != 1 || model.ExperimentalSupportedTools[0] != "web_search" {
		t.Fatalf("model Rust metadata fields = %#v", model)
	}
	if model.AvailabilityNux == nil || model.AvailabilityNux.Message != "Welcome to GPT Test" {
		t.Fatalf("model availability_nux = %#v", model.AvailabilityNux)
	}
	if !model.NodeReplAutoReviewRequired || !model.NodeReplDisabled {
		t.Fatalf("node repl policy = %#v", model)
	}
	// Rust #51223: legacy `instructions_variables` are accepted but ignored, so
	// the literal template survives and personality stays unsupported.
	if model.ModelMessages == nil || model.ModelMessages.InstructionsTemplate != "Hello {{ personality }}" {
		t.Fatalf("model messages = %#v", model.ModelMessages)
	}
	if model.SupportsPersonality() {
		t.Fatal("legacy instructions_variables must not enable personality")
	}
	if model.ModelMessages.CollaborationModes == nil || model.ModelMessages.CollaborationModes.Default == nil || *model.ModelMessages.CollaborationModes.Default != "" || model.ModelMessages.CollaborationModes.Plan != nil {
		t.Fatalf("collaboration mode messages = %#v", model.ModelMessages.CollaborationModes)
	}
	if model.ModelMessages.TokenBudget == nil || model.ModelMessages.TokenBudget.GuidanceMessage != "keep notes" || model.ModelMessages.TokenBudget.AutoCompactFallbackBufferTokens != 8000 {
		t.Fatalf("model token budget = %#v", model.ModelMessages.TokenBudget)
	}
	cloned := cloneModelInfo(model)
	cloned.ModelMessages.TokenBudget.GuidanceMessage = "changed"
	changedDefault := "changed"
	cloned.ModelMessages.CollaborationModes.Default = &changedDefault
	if model.ModelMessages.TokenBudget.GuidanceMessage != "keep notes" {
		t.Fatalf("clone shares model token budget = %#v", model.ModelMessages.TokenBudget)
	}
	if model.ModelMessages.CollaborationModes.Default == nil || *model.ModelMessages.CollaborationModes.Default != "" {
		t.Fatalf("clone shares collaboration messages = %#v", model.ModelMessages.CollaborationModes)
	}
}

func TestModelMessagesMultiAgentParsingAndOverridePreservationLikeRust(t *testing.T) {
	var messages ModelMessages
	if err := json.Unmarshal([]byte(`{
		"instructions_template": null,
		"instructions_variables": null,
		"multi_agent": {
			"role": {"root": "", "subagent": "subagent base"},
			"mode": {"explicit": "explicit mode", "hint_text": ""}
		}
	}`), &messages); err != nil {
		t.Fatalf("Unmarshal multi-agent messages error = %v", err)
	}
	if messages.MultiAgent == nil || messages.MultiAgent.Role == nil || messages.MultiAgent.Mode == nil {
		t.Fatalf("multi-agent messages = %#v", messages.MultiAgent)
	}
	if messages.MultiAgent.Role.Root == nil || *messages.MultiAgent.Role.Root != "" {
		t.Fatalf("empty root role = %#v", messages.MultiAgent.Role.Root)
	}
	if messages.MultiAgent.Role.Subagent == nil || *messages.MultiAgent.Role.Subagent != "subagent base" {
		t.Fatalf("subagent role = %#v", messages.MultiAgent.Role.Subagent)
	}
	if messages.MultiAgent.Mode.Explicit == nil || *messages.MultiAgent.Mode.Explicit != "explicit mode" {
		t.Fatalf("explicit mode = %#v", messages.MultiAgent.Mode.Explicit)
	}
	if messages.MultiAgent.Mode.HintText == nil || *messages.MultiAgent.Mode.HintText != "" {
		t.Fatalf("empty hint text = %#v", messages.MultiAgent.Mode.HintText)
	}

	// A base-instructions override replaces the message set, clearing the
	// catalog-provided multi-agent messages (Rust with_config_overrides).
	model := ModelInfo{
		Slug:          "gpt-test",
		ModelMessages: &ModelMessages{MultiAgent: messages.MultiAgent},
	}
	overridden := WithConfigOverrides(model, &ModelsManagerConfig{BaseInstructions: modelsManagerStringPtr("override")})
	if overridden.ModelMessages == nil || overridden.ModelMessages.MultiAgent != nil {
		t.Fatalf("base-instructions override retained multi-agent messages: %#v", overridden.ModelMessages)
	}
	if overridden.ModelMessages.InstructionsTemplate != "override" {
		t.Fatalf("instructions template = %q", overridden.ModelMessages.InstructionsTemplate)
	}
}

func TestModelMessagesAutoReviewParsingPreservesEmptyOverridesLikeRust(t *testing.T) {
	var messages ModelMessages
	if err := json.Unmarshal([]byte(`{
		"auto_review": {
			"policy": "policy",
			"policy_template": "",
			"node_repl_policy": "node repl rules",
			"rejection_instructions": "reject instructions",
			"timeout_instructions": "timeout instructions"
		}
	}`), &messages); err != nil {
		t.Fatalf("Unmarshal auto_review messages error = %v", err)
	}
	if messages.AutoReview == nil {
		t.Fatal("auto_review messages not parsed")
	}
	autoReview := messages.AutoReview
	if autoReview.Policy == nil || *autoReview.Policy != "policy" {
		t.Fatalf("policy = %#v", autoReview.Policy)
	}
	if autoReview.PolicyTemplate == nil || *autoReview.PolicyTemplate != "" {
		t.Fatalf("empty policy_template override lost: %#v", autoReview.PolicyTemplate)
	}
	if autoReview.NodeReplPolicy == nil || *autoReview.NodeReplPolicy != "node repl rules" {
		t.Fatalf("node_repl_policy = %#v", autoReview.NodeReplPolicy)
	}
	if autoReview.RejectionInstructions == nil || *autoReview.RejectionInstructions != "reject instructions" {
		t.Fatalf("rejection_instructions = %#v", autoReview.RejectionInstructions)
	}
	if autoReview.TimeoutInstructions == nil || *autoReview.TimeoutInstructions != "timeout instructions" {
		t.Fatalf("timeout_instructions = %#v", autoReview.TimeoutInstructions)
	}
	model := ModelInfo{Slug: "gpt-test", ModelMessages: &messages}
	cloned := cloneModelInfo(model)
	if cloned.ModelMessages == nil || cloned.ModelMessages.AutoReview == nil || *cloned.ModelMessages.AutoReview.RejectionInstructions != "reject instructions" {
		t.Fatalf("cloned auto_review messages = %#v", cloned.ModelMessages)
	}
}

func TestModelMessagesConfirmationPoliciesRoundTrip(t *testing.T) {
	browser := "# Browser confirmations\n\nKeep {{literal_markdown}}.\n"
	computer := "  # Native confirmations\r\n\nKeep ${native_markdown}.\n"
	messages := ModelMessages{
		ConfirmationPolicies: &ConfirmationPolicies{
			BrowserUse:  &browser,
			ComputerUse: &computer,
		},
	}
	data, err := json.Marshal(&messages)
	if err != nil {
		t.Fatalf("marshal confirmation_policies messages error = %v", err)
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("unmarshal marshaled value error = %v", err)
	}
	policies, ok := value["confirmation_policies"].(map[string]any)
	if !ok {
		t.Fatalf("confirmation_policies not serialized: %s", string(data))
	}
	if policies["browser_use"] != browser || policies["computer_use"] != computer {
		t.Fatalf("confirmation_policies = %#v", policies)
	}

	var parsed ModelMessages
	if err := json.Unmarshal([]byte(`{"confirmation_policies":{"browser_use":"b","computer_use":"c"}}`), &parsed); err != nil {
		t.Fatalf("unmarshal confirmation_policies messages error = %v", err)
	}
	if parsed.ConfirmationPolicies == nil ||
		parsed.ConfirmationPolicies.BrowserUse == nil || *parsed.ConfirmationPolicies.BrowserUse != "b" ||
		parsed.ConfirmationPolicies.ComputerUse == nil || *parsed.ConfirmationPolicies.ComputerUse != "c" {
		t.Fatalf("parsed confirmation_policies = %#v", parsed.ConfirmationPolicies)
	}

	// A base-instructions override replaces the message set, dropping the
	// catalog-provided confirmation-policy documents (Rust #41072).
	model := ModelInfo{Slug: "gpt-test", ModelMessages: &messages}
	overridden := WithConfigOverrides(model, &ModelsManagerConfig{BaseInstructions: modelsManagerStringPtr("override")})
	if overridden.ModelMessages == nil || overridden.ModelMessages.ConfirmationPolicies != nil {
		t.Fatalf("base-instructions override retained confirmation_policies: %#v", overridden.ModelMessages)
	}
}

// TestModelMessagesMultiAgentToolOverridesLikeRust mirrors Rust #46505's catalog
// resolution: the model messages carry per-tool Multi-Agent V2 descriptions and
// JSON-encoded parameter schemas, selected by tool name independently of the
// runtime namespace; absent entries resolve to nil.
func TestModelMessagesMultiAgentToolOverridesLikeRust(t *testing.T) {
	var messages ModelMessages
	document := `{
		"tools": {
			"multi_agent": {
				"spawn_agent": {"description": "Catalog spawn text.", "parameters": "{\"type\":\"object\"}"},
				"list_agents": {"description": ""},
				"wait_agent": {"parameters": "{\"type\":\"object\",\"properties\":{}}"}
			}
		}
	}`
	if err := json.Unmarshal([]byte(document), &messages); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if got := messages.MultiAgentToolDescriptionOverride("spawn_agent"); got == nil || *got != "Catalog spawn text." {
		t.Fatalf("spawn_agent description = %#v", got)
	}
	if got := messages.MultiAgentToolParametersOverride("spawn_agent"); got == nil || *got != `{"type":"object"}` {
		t.Fatalf("spawn_agent parameters = %#v", got)
	}
	// An explicit empty description is preserved (the tool suppresses its text).
	if got := messages.MultiAgentToolDescriptionOverride("list_agents"); got == nil || *got != "" {
		t.Fatalf("list_agents description = %#v", got)
	}
	if got := messages.MultiAgentToolDescriptionOverride("wait_agent"); got != nil {
		t.Fatalf("wait_agent description = %#v, want nil", got)
	}
	if got := messages.MultiAgentToolParametersOverride("wait_agent"); got == nil {
		t.Fatal("wait_agent parameters = nil")
	}
	// Tools without a catalog entry resolve to nil, and unknown names never match.
	if messages.MultiAgentToolMessage("send_message") != nil || messages.MultiAgentToolMessage("unknown_tool") != nil ||
		messages.MultiAgentToolDescriptionOverride("followup_task") != nil {
		t.Fatalf("unexpected catalog entries: %#v", messages.Tools)
	}
	// The catalog copy must not share the override pointers with the original.
	cloned := cloneModelInfo(ModelInfo{ModelMessages: &messages})
	if cloned.ModelMessages == nil || cloned.ModelMessages.Tools == nil || cloned.ModelMessages.Tools.MultiAgent == nil {
		t.Fatalf("cloned messages = %#v", cloned.ModelMessages)
	}
	original := messages.MultiAgentToolDescriptionOverride("spawn_agent")
	clonedDescription := cloned.ModelMessages.MultiAgentToolDescriptionOverride("spawn_agent")
	if clonedDescription == original {
		t.Fatal("the clone shares the catalog description pointer")
	}
	*clonedDescription = "rewritten"
	if *messages.MultiAgentToolDescriptionOverride("spawn_agent") != "Catalog spawn text." {
		t.Fatalf("mutating the clone changed the original: %#v", messages.Tools.MultiAgent.SpawnAgent)
	}
}

func TestModelMessagesToolMessagesRoundTrip(t *testing.T) {
	desc := "Ask the user a clarifying question."
	messages := ModelMessages{
		Tools: &ToolMessages{
			SendUserMessageAsync: &ToolMessage{Description: &desc},
		},
	}
	data, err := json.Marshal(&messages)
	if err != nil {
		t.Fatalf("marshal tools messages error = %v", err)
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("unmarshal marshaled value error = %v", err)
	}
	tools, ok := value["tools"].(map[string]any)
	if !ok {
		t.Fatalf("tools not serialized: %s", string(data))
	}
	tool, ok := tools["send_user_message_async"].(map[string]any)
	if !ok || tool["description"] != desc {
		t.Fatalf("send_user_message_async tool = %#v", tools)
	}

	// A missing description falls back to a nil pointer (built-in), while an
	// explicit empty string is preserved (Rust #41461).
	var missing ModelMessages
	if err := json.Unmarshal([]byte(`{"tools":{"send_user_message_async":{}}}`), &missing); err != nil {
		t.Fatalf("unmarshal tools messages error = %v", err)
	}
	if missing.Tools == nil || missing.Tools.SendUserMessageAsync == nil || missing.Tools.SendUserMessageAsync.Description != nil {
		t.Fatalf("missing description tools = %#v", missing.Tools)
	}
	var empty ModelMessages
	if err := json.Unmarshal([]byte(`{"tools":{"send_user_message_async":{"description":""}}}`), &empty); err != nil {
		t.Fatalf("unmarshal empty description tools error = %v", err)
	}
	if empty.Tools == nil || empty.Tools.SendUserMessageAsync == nil || empty.Tools.SendUserMessageAsync.Description == nil || *empty.Tools.SendUserMessageAsync.Description != "" {
		t.Fatalf("empty description tools = %#v", empty.Tools)
	}
}

func TestModelMessagesMultiAgentProactiveRoundTrip(t *testing.T) {
	proactive := "Use proactive delegation from the model catalog."
	messages := ModelMessages{
		MultiAgent: &MultiAgentMessages{
			Mode: &MultiAgentModeMessages{Proactive: &proactive},
		},
	}
	data, err := json.Marshal(&messages)
	if err != nil {
		t.Fatalf("marshal multi-agent proactive messages error = %v", err)
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("unmarshal marshaled value error = %v", err)
	}
	multiAgent, ok := value["multi_agent"].(map[string]any)
	if !ok {
		t.Fatalf("multi_agent not serialized: %s", string(data))
	}
	mode, ok := multiAgent["mode"].(map[string]any)
	if !ok {
		t.Fatalf("multi_agent.mode not serialized: %s", string(data))
	}
	if mode["proactive"] != proactive {
		t.Fatalf("multi_agent.mode = %#v, want proactive %q", mode, proactive)
	}

	var parsed ModelMessages
	if err := json.Unmarshal([]byte(`{"multi_agent":{"mode":{"proactive":"Use proactive delegation from the model catalog."}}}`), &parsed); err != nil {
		t.Fatalf("unmarshal multi-agent proactive messages error = %v", err)
	}
	if parsed.MultiAgent == nil || parsed.MultiAgent.Mode == nil ||
		parsed.MultiAgent.Mode.Proactive == nil || *parsed.MultiAgent.Mode.Proactive != proactive {
		t.Fatalf("parsed multi-agent proactive = %#v", parsed.MultiAgent)
	}
}

func TestServiceTierForRequest(t *testing.T) {
	info := &ModelInfo{ServiceTiers: []string{"priority", "flex"}}
	if got := ServiceTierForRequest(info, "fast"); got != "priority" {
		t.Fatalf("fast tier = %q", got)
	}
	if got := ServiceTierForRequest(info, "default"); got != "" {
		t.Fatalf("default tier = %q", got)
	}
	if got := ServiceTierForRequest(info, "turbo"); got != "" {
		t.Fatalf("unsupported tier = %q", got)
	}
	if got := ServiceTierForRequest(&ModelInfo{UsedFallbackModelMetadata: true}, "priority"); got != "" {
		t.Fatalf("fallback model tier = %q", got)
	}
}

func TestRemoteModelsManagerRefreshesOnlineAndETag(t *testing.T) {
	endpoint := &recordingModelsEndpoint{
		responses: []*ModelsEndpointResponse{
			{
				Models: []ModelInfo{{
					Slug:           "remote",
					DisplayName:    "Remote",
					Visibility:     VisibilityVisible,
					SupportedInAPI: true,
					Priority:       0,
				}},
				ETag: "etag-1",
			},
			{
				Models: []ModelInfo{{
					Slug:           "remote",
					DisplayName:    "Remote Updated",
					Visibility:     VisibilityVisible,
					SupportedInAPI: true,
					Priority:       0,
				}},
				ETag: "etag-2",
			},
		},
	}
	manager := NewRemoteModelsManager(&ModelsResponse{Models: []ModelInfo{{
		Slug:           "bundled",
		DisplayName:    "Bundled",
		Visibility:     VisibilityVisible,
		SupportedInAPI: true,
		Priority:       10,
	}}}, endpoint)

	offline := manager.ListModels(RefreshOffline)
	if len(offline) != 1 || offline[0].Model != "bundled" || endpoint.calls != 0 {
		t.Fatalf("offline models = %#v, calls = %d", offline, endpoint.calls)
	}
	online := manager.ListModels(RefreshOnlineIfUncached)
	if endpoint.calls != 1 {
		t.Fatalf("calls after online_if_uncached = %d", endpoint.calls)
	}
	if len(online) != 2 || online[0].Model != "remote" || online[1].Model != "bundled" {
		t.Fatalf("online models = %#v", online)
	}
	_ = manager.ListModels(RefreshOnlineIfUncached)
	if endpoint.calls != 1 {
		t.Fatalf("calls after cached online_if_uncached = %d", endpoint.calls)
	}
	manager.RefreshIfNewETag("etag-1")
	if endpoint.calls != 1 {
		t.Fatalf("calls after same etag = %d", endpoint.calls)
	}
	manager.RefreshIfNewETag("etag-2")
	if endpoint.calls != 2 || endpoint.etags[1] != "etag-1" {
		t.Fatalf("calls = %d etags = %#v", endpoint.calls, endpoint.etags)
	}
	updated := manager.GetModelInfo("remote", nil)
	if updated.DisplayName != "Remote Updated" {
		t.Fatalf("updated model = %#v", updated)
	}
}

// TestRemoteModelsManagerRefreshAfterAuthChangeMatchesRust mirrors Rust #46508:
// a catalog that no credential has claimed yet is refreshed before a turn,
// while a catalog that already belongs to the current identity is left alone,
// and hosts whose catalogs credentials cannot change never refresh.
func TestRemoteModelsManagerRefreshAfterAuthChangeMatchesRust(t *testing.T) {
	newManager := func(t *testing.T, options *RemoteModelsManagerOptions) (*RemoteModelsManager, *recordingModelsEndpoint) {
		t.Helper()
		endpoint := &recordingModelsEndpoint{responses: []*ModelsEndpointResponse{{
			Models: []ModelInfo{{
				Slug:           "remote",
				DisplayName:    "Remote",
				Visibility:     VisibilityVisible,
				SupportedInAPI: true,
			}},
			ETag: "etag-1",
		}}}
		opts := *options
		opts.Endpoint = endpoint
		return NewRemoteModelsManagerWithOptions(&opts), endpoint
	}
	base := &ModelsResponse{Models: []ModelInfo{{
		Slug:           "bundled",
		DisplayName:    "Bundled",
		Visibility:     VisibilityVisible,
		SupportedInAPI: true,
		Priority:       10,
	}}}

	// An unscoped catalog is claimed by the current credentials before the turn.
	manager, endpoint := newManager(t, &RemoteModelsManagerOptions{
		ModelCatalog: base,
		Identity:     "identity-a",
		CommandAuth:  true,
	})
	manager.RefreshAfterAuthChange()
	if endpoint.calls != 1 {
		t.Fatalf("calls after refreshing an unclaimed catalog = %d, want 1", endpoint.calls)
	}
	if info := manager.GetModelInfo("remote", nil); info.Slug != "remote" {
		t.Fatalf("catalog was not refreshed: %#v", info)
	}
	// The catalog now belongs to the current identity, so a second refresh is a
	// no-op.
	manager.RefreshAfterAuthChange()
	if endpoint.calls != 1 {
		t.Fatalf("calls after refreshing a matching catalog = %d, want 1", endpoint.calls)
	}

	// Hosts whose catalog does not follow credentials never refresh.
	staticManager, staticEndpoint := newManager(t, &RemoteModelsManagerOptions{ModelCatalog: base})
	staticManager.RefreshAfterAuthChange()
	if staticEndpoint.calls != 0 {
		t.Fatalf("calls for an uncredentialed catalog = %d, want 0", staticEndpoint.calls)
	}

	// A discovery failure leaves the existing catalog in place.
	failing := &recordingModelsEndpoint{}
	failingManager := NewRemoteModelsManagerWithOptions(&RemoteModelsManagerOptions{
		ModelCatalog: base,
		Endpoint:     failing,
		Identity:     "identity-a",
		CommandAuth:  true,
	})
	failingManager.RefreshAfterAuthChange()
	if info := failingManager.GetModelInfo("bundled", nil); info.Slug != "bundled" {
		t.Fatalf("fallback catalog = %#v", info)
	}
}

func TestRemoteModelsManagerThrottlesMatchingETagCacheRenewal(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 7, 29, 1, 0, 0, 0, time.UTC)
	endpoint := &recordingModelsEndpoint{responses: []*ModelsEndpointResponse{{
		Models: []ModelInfo{{Slug: "remote", DisplayName: "Remote", Visibility: VisibilityList, SupportedInAPI: true}},
		ETag:   "etag-1",
	}}}
	manager := NewRemoteModelsManagerWithOptions(&RemoteModelsManagerOptions{
		Endpoint:                        endpoint,
		UseRemoteCatalogAsSourceOfTruth: true,
		Identity:                        "identity-1",
	})
	manager.ConfigureCache(home)
	manager.now = func() time.Time { return now }

	_ = manager.ListModels(RefreshOnlineIfUncached)
	cachePath := filepath.Join(home, modelsCacheFilename)
	original, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	now = now.Add(time.Minute)
	manager.RefreshIfNewETag("etag-1")
	recent, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("ReadFile(recent) error = %v", err)
	}
	if !bytes.Equal(recent, original) {
		t.Fatal("matching ETag rewrote a cache younger than half its TTL")
	}

	now = now.Add(2 * time.Minute)
	manager.RefreshIfNewETag("etag-1")
	renewed, err := readModelsCache(cachePath)
	if err != nil {
		t.Fatalf("readModelsCache() error = %v", err)
	}
	if !renewed.FetchedAt.Equal(now) {
		t.Fatalf("renewed fetched_at = %s, want %s", renewed.FetchedAt, now)
	}
	if endpoint.calls != 1 {
		t.Fatalf("matching ETag refetched models: calls = %d", endpoint.calls)
	}
}

func TestRemoteModelsManagerLoadsFreshDiskCacheAndRejectsStaleOrWrongVersion(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 7, 29, 2, 0, 0, 0, time.UTC)
	cachePath := filepath.Join(home, modelsCacheFilename)
	cache := &modelsCache{
		FetchedAt:     now.Add(-time.Minute),
		ETag:          "etag-cached",
		ClientVersion: modelsEndpointClientVersion,
		Identity:      "identity-1",
		Models:        []ModelInfo{{Slug: "cached", DisplayName: "Cached", Visibility: VisibilityList, SupportedInAPI: true}},
	}
	if err := writeModelsCache(cachePath, cache); err != nil {
		t.Fatalf("writeModelsCache() error = %v", err)
	}

	onlineResponse := &ModelsEndpointResponse{
		Models: []ModelInfo{{Slug: "online", DisplayName: "Online", Visibility: VisibilityList, SupportedInAPI: true}},
		ETag:   "etag-online",
	}
	endpoint := &recordingModelsEndpoint{responses: []*ModelsEndpointResponse{onlineResponse, onlineResponse, onlineResponse, onlineResponse}}
	manager := NewRemoteModelsManagerWithOptions(&RemoteModelsManagerOptions{Endpoint: endpoint, UseRemoteCatalogAsSourceOfTruth: true, Identity: "identity-1"})
	manager.ConfigureCache(home)
	manager.now = func() time.Time { return now }
	models := manager.ListModels(RefreshOnlineIfUncached)
	if len(models) != 1 || models[0].Model != "cached" || endpoint.calls != 0 {
		t.Fatalf("fresh cache models = %#v, calls = %d", models, endpoint.calls)
	}

	cache.ClientVersion = "other-version"
	if err := writeModelsCache(cachePath, cache); err != nil {
		t.Fatalf("writeModelsCache(wrong version) error = %v", err)
	}
	manager = NewRemoteModelsManagerWithOptions(&RemoteModelsManagerOptions{Endpoint: endpoint, UseRemoteCatalogAsSourceOfTruth: true, Identity: "identity-1"})
	manager.ConfigureCache(home)
	manager.now = func() time.Time { return now }
	models = manager.ListModels(RefreshOnlineIfUncached)
	if len(models) != 1 || models[0].Model != "online" || endpoint.calls != 1 {
		t.Fatalf("wrong-version fallback models = %#v, calls = %d", models, endpoint.calls)
	}

	// A cache scoped to a different provider/auth identity is a miss.
	cache.ClientVersion = modelsEndpointClientVersion
	cache.Identity = "identity-2"
	if err := writeModelsCache(cachePath, cache); err != nil {
		t.Fatalf("writeModelsCache(mismatched identity) error = %v", err)
	}
	manager = NewRemoteModelsManagerWithOptions(&RemoteModelsManagerOptions{Endpoint: endpoint, UseRemoteCatalogAsSourceOfTruth: true, Identity: "identity-1"})
	manager.ConfigureCache(home)
	manager.now = func() time.Time { return now }
	models = manager.ListModels(RefreshOnlineIfUncached)
	if len(models) != 1 || models[0].Model != "online" || endpoint.calls != 2 {
		t.Fatalf("identity-mismatch fallback models = %#v, calls = %d", models, endpoint.calls)
	}

	// Unscoped legacy entries are cache misses.
	cache.Identity = ""
	if err := writeModelsCache(cachePath, cache); err != nil {
		t.Fatalf("writeModelsCache(legacy) error = %v", err)
	}
	manager = NewRemoteModelsManagerWithOptions(&RemoteModelsManagerOptions{Endpoint: endpoint, UseRemoteCatalogAsSourceOfTruth: true, Identity: "identity-1"})
	manager.ConfigureCache(home)
	manager.now = func() time.Time { return now }
	models = manager.ListModels(RefreshOnlineIfUncached)
	if len(models) != 1 || models[0].Model != "online" || endpoint.calls != 3 {
		t.Fatalf("legacy cache fallback models = %#v, calls = %d", models, endpoint.calls)
	}

	// A manager without an identity never reuses cached catalogs.
	manager = NewRemoteModelsManagerWithOptions(&RemoteModelsManagerOptions{Endpoint: endpoint, UseRemoteCatalogAsSourceOfTruth: true})
	manager.ConfigureCache(home)
	manager.now = func() time.Time { return now }
	models = manager.ListModels(RefreshOnlineIfUncached)
	if len(models) != 1 || models[0].Model != "online" || endpoint.calls != 4 {
		t.Fatalf("unscoped manager fallback models = %#v, calls = %d", models, endpoint.calls)
	}
}

func TestRemoteModelsManagerCanUseRemoteCatalogAsSourceOfTruth(t *testing.T) {
	endpoint := &recordingModelsEndpoint{
		responses: []*ModelsEndpointResponse{{
			Models: []ModelInfo{{
				Slug:           "chatgpt-remote",
				DisplayName:    "ChatGPT Remote",
				Visibility:     VisibilityList,
				SupportedInAPI: true,
				Priority:       0,
			}},
		}},
	}
	manager := NewRemoteModelsManagerWithOptions(&RemoteModelsManagerOptions{
		ModelCatalog: &ModelsResponse{Models: []ModelInfo{{
			Slug:           "bundled",
			DisplayName:    "Bundled",
			Visibility:     VisibilityVisible,
			SupportedInAPI: true,
			Priority:       10,
		}}},
		Endpoint:                        endpoint,
		UseRemoteCatalogAsSourceOfTruth: true,
	})

	models := manager.ListModels(RefreshOnlineIfUncached)
	if len(models) != 1 || models[0].Model != "chatgpt-remote" {
		t.Fatalf("models = %#v", models)
	}
}

func TestRemoteModelsManagerKeepsMergingForAPIAuthAndHiddenOnlyRemote(t *testing.T) {
	endpoint := &recordingModelsEndpoint{
		responses: []*ModelsEndpointResponse{{
			Models: []ModelInfo{{
				Slug:           "hidden-remote",
				DisplayName:    "Hidden Remote",
				Visibility:     VisibilityHide,
				SupportedInAPI: true,
				Priority:       0,
			}},
		}},
	}
	manager := NewRemoteModelsManagerWithOptions(&RemoteModelsManagerOptions{
		ModelCatalog: &ModelsResponse{Models: []ModelInfo{{
			Slug:           "bundled",
			DisplayName:    "Bundled",
			Visibility:     VisibilityVisible,
			SupportedInAPI: true,
			Priority:       10,
		}}},
		Endpoint:                        endpoint,
		UseRemoteCatalogAsSourceOfTruth: true,
	})

	models := manager.ListModels(RefreshOnlineIfUncached)
	if len(models) != 2 || models[0].Model != "hidden-remote" || models[1].Model != "bundled" {
		t.Fatalf("models = %#v", models)
	}
}

func TestHTTPModelsEndpointSendsHeadersAndParsesModels(t *testing.T) {
	var gotPath string
	var gotIfNoneMatch string
	var gotAuthorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.String()
		gotIfNoneMatch = r.Header.Get("If-None-Match")
		gotAuthorization = r.Header.Get("Authorization")
		w.Header().Set(modelsEndpointETagHeader, "etag-remote")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[{"slug":"remote","display_name":"Remote","visibility":"visible","supported_in_api":true,"priority":0}]}`))
	}))
	defer server.Close()

	endpoint := NewHTTPModelsEndpoint(
		&APIProvider{BaseURL: server.URL + "/v1", QueryParams: map[string]string{"api-version": "1"}},
		&AuthHeaders{Headers: http.Header{"Authorization": []string{"Bearer token"}}},
		server.Client(),
	)
	response, err := endpoint.ListModels(nil, "etag-local")
	if err != nil {
		t.Fatalf("ListModels returned error: %v", err)
	}
	parsedPath, err := url.Parse(gotPath)
	if err != nil {
		t.Fatalf("request path parse error: %v", err)
	}
	query := parsedPath.Query()
	if parsedPath.Path != "/v1/models" || query.Get("api-version") != "1" || query.Get("client_version") != "0.0.0" || gotIfNoneMatch != "etag-local" || gotAuthorization != "Bearer token" {
		t.Fatalf("request path=%q if-none-match=%q authorization=%q", gotPath, gotIfNoneMatch, gotAuthorization)
	}
	if response.ETag != "etag-remote" || len(response.Models) != 1 || response.Models[0].Slug != "remote" {
		t.Fatalf("response = %#v", response)
	}
}

func TestHTTPModelsEndpointAlwaysSendsClientVersion(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.String()
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()

	endpoint := NewHTTPModelsEndpoint(&APIProvider{BaseURL: server.URL + "/v1"}, nil, server.Client())
	if _, err := endpoint.ListModels(nil, ""); err != nil {
		t.Fatalf("ListModels returned error: %v", err)
	}
	parsedPath, err := url.Parse(gotPath)
	if err != nil {
		t.Fatalf("request path parse error: %v", err)
	}
	if parsedPath.Path != "/v1/models" || parsedPath.Query().Get("client_version") != "0.0.0" {
		t.Fatalf("path = %q", gotPath)
	}
}

func TestHTTPModelsEndpointAppliesRequestSigner(t *testing.T) {
	var gotAuthorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthorization = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()

	endpoint := NewHTTPModelsEndpoint(
		&APIProvider{BaseURL: server.URL + "/v1"},
		&AuthHeaders{
			SignRequest: func(_ context.Context, request *http.Request, body []byte) (*SignedRequest, error) {
				request.Header.Set("Authorization", "Signed models")
				return &SignedRequest{Body: body}, nil
			},
		},
		server.Client(),
	)
	if _, err := endpoint.ListModels(nil, ""); err != nil {
		t.Fatalf("ListModels returned error: %v", err)
	}
	if gotAuthorization != "Signed models" {
		t.Fatalf("Authorization = %q", gotAuthorization)
	}
}

func TestStaticModelsManagerDefaultModelPolicy(t *testing.T) {
	manager := NewStaticModelsManager(ModelsResponse{Models: []ModelInfo{
		{Slug: "default-model", DisplayName: "default-model", Visibility: VisibilityVisible, SupportedInAPI: true, Priority: 0},
		{Slug: "requested-model", DisplayName: "requested-model", Visibility: VisibilityVisible, SupportedInAPI: true, Priority: 10},
	}})

	if got := manager.GetDefaultModel("", false, RefreshOffline); got != "default-model" {
		t.Fatalf("default model = %q", got)
	}
	if got := manager.GetDefaultModel("missing-model", false, RefreshOffline); got != "missing-model" {
		t.Fatalf("preserved model = %q", got)
	}
	if got := manager.GetDefaultModel("missing-model", true, RefreshOffline); got != "default-model" {
		t.Fatalf("fallback model = %q", got)
	}
	if got := manager.GetDefaultModel("requested-model", true, RefreshOffline); got != "requested-model" {
		t.Fatalf("available requested model = %q", got)
	}
}

func TestConstructModelInfoUsesLongestPrefix(t *testing.T) {
	candidates := []ModelInfo{
		{Slug: "gpt-5", DisplayName: "gpt-5", Visibility: VisibilityVisible, SupportedInAPI: true, Priority: 10},
		{Slug: "gpt-5.3-codex", DisplayName: "codex", Visibility: VisibilityVisible, SupportedInAPI: true, Priority: 0},
	}
	info := ConstructModelInfoFromCandidates("gpt-5.3-codex-special", candidates, nil)
	if info.DisplayName != "codex" {
		t.Fatalf("DisplayName = %q", info.DisplayName)
	}
	if info.Slug != "gpt-5.3-codex-special" {
		t.Fatalf("Slug = %q", info.Slug)
	}
	if info.UsedFallbackModelMetadata {
		t.Fatal("UsedFallbackModelMetadata = true, want false")
	}
}

func TestConstructModelInfoUsesNamespacedSuffix(t *testing.T) {
	candidates := []ModelInfo{
		{Slug: "gpt-5.3-codex", DisplayName: "codex", Visibility: VisibilityVisible, SupportedInAPI: true, Priority: 0},
	}
	info := ConstructModelInfoFromCandidates("custom/gpt-5.3-codex", candidates, nil)
	if info.DisplayName != "codex" {
		t.Fatalf("DisplayName = %q", info.DisplayName)
	}
	if info.Slug != "custom/gpt-5.3-codex" {
		t.Fatalf("Slug = %q", info.Slug)
	}
}

func TestConstructModelInfoFallsBackForUnknownModel(t *testing.T) {
	info := ConstructModelInfoFromCandidates("unknown-model", nil, nil)
	if !info.UsedFallbackModelMetadata {
		t.Fatal("UsedFallbackModelMetadata = false, want true")
	}
	if info.DisplayName != "unknown-model" {
		t.Fatalf("DisplayName = %q", info.DisplayName)
	}
}

func TestWithConfigOverrides(t *testing.T) {
	supportsReasoningSummaries := true
	model := ModelInfoFromSlug("unknown-model")
	model.MaxContextWindow = 400000
	model.TruncationPolicy = TruncationPolicy{Mode: TruncationModeTokens, Limit: 10}

	updated := WithConfigOverrides(model, &ModelsManagerConfig{
		ModelContextWindow:              500000,
		ModelAutoCompactTokenLimit:      12345,
		ToolOutputTokenLimit:            456,
		BaseInstructions:                modelsManagerStringPtr("custom instructions"),
		ModelSupportsReasoningSummaries: &supportsReasoningSummaries,
	})

	if !updated.SupportsReasoningSummaries {
		t.Fatal("SupportsReasoningSummaries = false, want true")
	}
	if updated.ContextWindow != 400000 {
		t.Fatalf("ContextWindow = %d", updated.ContextWindow)
	}
	if updated.AutoCompactTokenLimit != 12345 {
		t.Fatalf("AutoCompactTokenLimit = %d", updated.AutoCompactTokenLimit)
	}
	if updated.TruncationPolicy.Mode != TruncationModeTokens || updated.TruncationPolicy.Limit != 456 {
		t.Fatalf("TruncationPolicy = %#v", updated.TruncationPolicy)
	}
	if updated.BaseInstructions != "custom instructions" {
		t.Fatalf("BaseInstructions = %q", updated.BaseInstructions)
	}
	if updated.ModelMessages == nil || updated.ModelMessages.InstructionsTemplate != "custom instructions" {
		t.Fatalf("ModelMessages = %#v, want instructions_template = custom instructions", updated.ModelMessages)
	}
}

func TestPersonalityDisabledFallsBackToBaseInstructionsForLocalPersonalityModels(t *testing.T) {
	model := ModelInfoFromSlug("gpt-5.2-codex")
	if model.ModelMessages == nil {
		t.Fatal("ModelMessages is nil before override")
	}
	updated := WithConfigOverrides(model, &ModelsManagerConfig{})
	if updated.ModelMessages == nil || updated.ModelMessages.InstructionsTemplate != BaseInstructions {
		t.Fatalf("ModelMessages = %#v, want instructions_template = BaseInstructions", updated.ModelMessages)
	}
}

func TestRetiredPersonalityLeavesLegacyTemplateLiteral(t *testing.T) {
	model := ModelInfo{
		BaseInstructions: "base",
		ModelMessages: &ModelMessages{
			InstructionsTemplate: "Hello {{ personality }}",
		},
	}
	updated := WithConfigOverrides(model, &ModelsManagerConfig{})
	if updated.ModelMessages == nil || updated.ModelMessages.InstructionsTemplate != "Hello {{ personality }}" {
		t.Fatalf("ModelMessages = %#v, want the literal legacy template", updated.ModelMessages)
	}
}

func TestInstructionOverridesPreserveCollaborationModeMessages(t *testing.T) {
	defaultInstructions := "catalog default"
	planInstructions := "catalog plan"
	for _, test := range []struct {
		name                 string
		config               *ModelsManagerConfig
		wantInstructionsTmpl string
	}{
		{name: "base instructions", config: &ModelsManagerConfig{BaseInstructions: modelsManagerStringPtr("override")}, wantInstructionsTmpl: "override"},
		{name: "personality none", config: &ModelsManagerConfig{Personality: "none"}, wantInstructionsTmpl: "Hello {{ personality }}"},
		{name: "no personality selection", config: &ModelsManagerConfig{}, wantInstructionsTmpl: "Hello {{ personality }}"},
	} {
		t.Run(test.name, func(t *testing.T) {
			info := ModelInfo{
				BaseInstructions: "base",
				ModelMessages: &ModelMessages{
					InstructionsTemplate: "Hello {{ personality }}",
					CollaborationModes: &CollaborationModeMessages{
						Default: &defaultInstructions,
						Plan:    &planInstructions,
					},
				},
			}
			updated := WithConfigOverrides(info, test.config)
			if updated.ModelMessages == nil || updated.ModelMessages.CollaborationModes == nil || updated.ModelMessages.CollaborationModes.Default == nil || *updated.ModelMessages.CollaborationModes.Default != defaultInstructions || updated.ModelMessages.CollaborationModes.Plan == nil || *updated.ModelMessages.CollaborationModes.Plan != planInstructions {
				t.Fatalf("collaboration messages were not preserved: %#v", updated.ModelMessages)
			}
			if updated.ModelMessages.InstructionsTemplate != test.wantInstructionsTmpl {
				t.Fatalf("instructions_template = %q, want %q", updated.ModelMessages.InstructionsTemplate, test.wantInstructionsTmpl)
			}
		})
	}
}

func TestModelInstructionsIgnoresLegacyPersonalityVariables(t *testing.T) {
	info := ModelInfo{
		BaseInstructions: "base",
		ModelMessages: &ModelMessages{
			InstructionsTemplate: "Hello {{ personality }}",
		},
	}
	for _, personality := range []string{"friendly", "pragmatic", "none", ""} {
		if got := info.ModelInstructions(personality); got != "Hello {{ personality }}" {
			t.Fatalf("%q instructions = %q, want the literal template", personality, got)
		}
	}
	if info.SupportsPersonality() {
		t.Fatal("SupportsPersonality = true, want false after Rust #44946")
	}

	info.ModelMessages.InstructionsTemplate = ""
	if got := info.ModelInstructions("friendly"); got != "base" {
		t.Fatalf("missing template instructions = %q", got)
	}
}

func TestPersonalityNoneStripsBakedPersonalitySection(t *testing.T) {
	template := "# Intro\nhello\n\n# Personality\nbe nice\n\n# Tools\nuse tools\n"
	newModel := func() ModelInfo {
		return ModelInfo{
			BaseInstructions: "base",
			ModelMessages:    &ModelMessages{InstructionsTemplate: template},
		}
	}

	stripped := WithConfigOverrides(newModel(), &ModelsManagerConfig{Personality: "none"})
	if stripped.ModelMessages == nil || stripped.ModelMessages.InstructionsTemplate != "# Intro\nhello\n\n# Tools\nuse tools\n" {
		t.Fatalf("personality-none template = %#v", stripped.ModelMessages)
	}

	// Any other selection (or no selection) keeps the literal template.
	for _, config := range []*ModelsManagerConfig{
		{Personality: "friendly"},
		{Personality: "pragmatic"},
		{},
	} {
		kept := WithConfigOverrides(newModel(), config)
		if kept.ModelMessages == nil || kept.ModelMessages.InstructionsTemplate != template {
			t.Fatalf("config %#v template = %#v", config, kept.ModelMessages)
		}
	}

	// A trailing personality section is stripped to the end of the template.
	trailing := ModelInfo{
		BaseInstructions: "base",
		ModelMessages:    &ModelMessages{InstructionsTemplate: "# Intro\nhi\n\n# Personality\nbe nice\n"},
	}
	strippedTrailing := WithConfigOverrides(trailing, &ModelsManagerConfig{Personality: "none"})
	if strippedTrailing.ModelMessages.InstructionsTemplate != "# Intro\nhi\n\n" {
		t.Fatalf("trailing personality template = %q", strippedTrailing.ModelMessages.InstructionsTemplate)
	}
}

// TestExplicitEmptyBaseInstructionsStayEmptyWithPersonalityNone mirrors Rust's
// `explicit_empty_base_instructions_stay_empty_with_personality_none` (#45809):
// an explicit empty base-instructions override is honored literally and does not
// fall back to the personality-stripped template.
func TestExplicitEmptyBaseInstructionsStayEmptyWithPersonalityNone(t *testing.T) {
	model := ModelInfo{
		BaseInstructions: "base",
		ModelMessages: &ModelMessages{
			InstructionsTemplate: "Intro\n# Personality\nRemove me",
		},
	}
	updated := WithConfigOverrides(model, &ModelsManagerConfig{
		BaseInstructions: modelsManagerStringPtr(""),
		Personality:      "none",
	})
	if updated.ModelMessages == nil || updated.ModelMessages.InstructionsTemplate != "" {
		t.Fatalf("instructions template = %#v, want empty", updated.ModelMessages)
	}
	if got := updated.ModelInstructions("none"); got != "" {
		t.Fatalf("ModelInstructions(none) = %q, want empty", got)
	}
}

// TestBakedPersonalitySectionIsPreservedWithoutExplicitNone mirrors Rust's
// `baked_personality_section_is_preserved_without_explicit_none` (#45809).
func TestBakedPersonalitySectionIsPreservedWithoutExplicitNone(t *testing.T) {
	template := "Intro\n# Personality\nKeep me\n# General\nKeep me too"
	for _, config := range []*ModelsManagerConfig{
		{},
		{Personality: "friendly"},
		{Personality: "pragmatic"},
	} {
		model := ModelInfo{BaseInstructions: "base", ModelMessages: &ModelMessages{InstructionsTemplate: template}}
		updated := WithConfigOverrides(model, config)
		if updated.ModelMessages == nil || updated.ModelMessages.InstructionsTemplate != template {
			t.Fatalf("config %#v template = %#v", config, updated.ModelMessages)
		}
	}
}

func modelsManagerStringPtr(value string) *string { return &value }

func TestModelInstructionsFixedTemplateIgnoresPersonality(t *testing.T) {
	// Rust #44930: bundled GPT-5.4 and GPT-5.5 replaced their selectable
	// personality templates with fixed friendly instructions, so personality is
	// unsupported and a submitted personality override leaves them unchanged.
	info := ModelInfo{
		BaseInstructions: "base",
		ModelMessages: &ModelMessages{
			InstructionsTemplate: "You are Codex, a coding agent based on GPT-5.",
		},
	}
	if info.SupportsPersonality() {
		t.Fatal("fixed-instruction model must not support personality")
	}
	for _, personality := range []string{"friendly", "pragmatic", "none", "default", ""} {
		if got := info.ModelInstructions(personality); got != "You are Codex, a coding agent based on GPT-5." {
			t.Fatalf("ModelInstructions(%q) = %q", personality, got)
		}
	}
}

func TestBundledCatalogFixedTemplatesReportNoPersonalitySupport(t *testing.T) {
	catalog, err := loadBundledModelsResponse()
	if err != nil {
		t.Skipf("bundled Rust catalog unavailable: %v", err)
	}
	manager := NewStaticModelsManager(catalog)
	for _, slug := range []string{"gpt-5.4", "gpt-5.5"} {
		info := manager.GetModelInfo(slug, nil)
		if info.ModelMessages == nil || strings.Contains(info.ModelMessages.InstructionsTemplate, personalityPlaceholder) {
			// Older catalog revisions still ship selectable personality templates.
			continue
		}
		if info.SupportsPersonality() {
			t.Fatalf("%s fixed instructions still support personality", slug)
		}
		if got := info.ModelInstructions("pragmatic"); got != info.ModelMessages.InstructionsTemplate {
			t.Fatalf("%s ModelInstructions(pragmatic) = %q", slug, got)
		}
	}
}

func TestAmazonBedrockModelCatalog(t *testing.T) {
	manager := NewStaticModelsManager(AmazonBedrockModelCatalog())
	models := manager.ListModels(RefreshOffline)
	want := []string{
		AmazonBedrockGPT61SolModelID,
		AmazonBedrockGPT6AstraModelID,
		AmazonBedrockGPT6SolModelID,
		AmazonBedrockGPT6LunaModelID,
		AmazonBedrockGPT56SolModelID,
		AmazonBedrockGPT56TerraModelID,
		AmazonBedrockGPT56LunaModelID,
		AmazonBedrockGPT55ModelID,
	}
	if len(models) != len(want) {
		t.Fatalf("models len = %d", len(models))
	}
	for i, wantModel := range want {
		if models[i].Model != wantModel {
			t.Fatalf("model[%d] = %q, want %q", i, models[i].Model, wantModel)
		}
	}
	if !models[0].IsDefault {
		t.Fatal("first Bedrock model should be default")
	}
	// Rust #49345 (8ffd91e42a) stopped forcing multi-agent V1 in the Bedrock
	// normalizer, so every entry keeps the `multi_agent_version` its bundled
	// OpenAI model declares. Go's fallback bundled catalog carries the GPT-6
	// and GPT-5.6 families (GPT-5.5 and the daybreak slugs are still absent
	// from it), so GPT-6.* and GPT-5.6 Sol/Terra stay V2 while GPT-5.6 Luna
	// stays V1 — the same preservation rule Rust applies.
	wantVersion := map[string]string{
		AmazonBedrockGPT61SolModelID:   "v2",
		AmazonBedrockGPT6AstraModelID:  "v2",
		AmazonBedrockGPT6SolModelID:    "v2",
		AmazonBedrockGPT6LunaModelID:   "v2",
		AmazonBedrockGPT56SolModelID:   "v2",
		AmazonBedrockGPT56TerraModelID: "v2",
		AmazonBedrockGPT56LunaModelID:  "v1",
		AmazonBedrockGPT55ModelID:      "",
	}
	for _, model := range models {
		want, ok := wantVersion[model.Model]
		if !ok {
			t.Fatalf("unexpected Bedrock model %s", model.Model)
		}
		if model.MultiAgentVersion != want {
			t.Fatalf("Bedrock model %s multi_agent_version = %q, want %q", model.Model, model.MultiAgentVersion, want)
		}
	}
}

// Mirrors Rust #49345 (8ffd91e42a)
// `configured_bedrock_catalogs_normalize_unsupported_model_capabilities` in
// codex-rs/model-provider/src/amazon_bedrock/catalog.rs: a configured catalog
// keeps its own `multi_agent_version` (V2 / V1 / Disabled / unset) while the
// normalizer only rewrites the wire-compatibility `web_search_tool_type`.
func TestNormalizeBedrockCatalogPreservesConfiguredMultiAgentVersionLikeRust(t *testing.T) {
	versions := []string{"v2", "v1", "disabled", ""}
	models := make([]ModelInfo, 0, len(versions))
	for _, version := range versions {
		models = append(models, ModelInfo{
			Slug:                     "configured-" + version,
			WebSearchToolType:        "text_and_image",
			MultiAgentVersion:        version,
			ServiceTiers:             []string{"priority"},
			DefaultServiceTier:       "priority",
			AdditionalSpeedTiers:     []string{"fast"},
			SupportedReasoningLevels: []string{"low", "ultra"},
		})
	}
	normalized := normalizeBedrockCatalog(ModelsResponse{Models: models})
	if len(normalized.Models) != len(models) {
		t.Fatalf("models len = %d, want %d", len(normalized.Models), len(models))
	}
	for i, model := range normalized.Models {
		if model.MultiAgentVersion != versions[i] {
			t.Fatalf("model[%d] multi_agent_version = %q, want %q", i, model.MultiAgentVersion, versions[i])
		}
		if model.WebSearchToolType != "text" {
			t.Fatalf("model[%d] WebSearchToolType = %q, want text", i, model.WebSearchToolType)
		}
		// A configured catalog owns its own speed/service/default tier definitions.
		if len(model.ServiceTiers) != 1 || model.ServiceTiers[0] != "priority" || model.DefaultServiceTier != "priority" {
			t.Fatalf("model[%d] tiers = %#v/%q", i, model.ServiceTiers, model.DefaultServiceTier)
		}
		if len(model.AdditionalSpeedTiers) != 1 || model.AdditionalSpeedTiers[0] != "fast" {
			t.Fatalf("model[%d] AdditionalSpeedTiers = %#v", i, model.AdditionalSpeedTiers)
		}
	}
}

// Mirrors Rust #49345 (8ffd91e42a) `bedrock_model`: the Bedrock builder keeps
// the Ultra reasoning level its bundled OpenAI model declares instead of
// stripping it, so the Astra advanced-reasoning picker offers Ultra.
func TestBedrockModelPreservesUltraReasoningLikeRust(t *testing.T) {
	bundled := ModelsResponse{Models: []ModelInfo{{
		Slug:                     "gpt-6-astra",
		Visibility:               VisibilityList,
		SupportedReasoningLevels: []string{"low", "medium", "ultra"},
	}}}
	model := bedrockModel(bundled, "gpt-6-astra", "openai.gpt-6-astra", "GPT-6-Astra", 1)
	want := []string{"low", "medium", "ultra"}
	if len(model.SupportedReasoningLevels) != len(want) {
		t.Fatalf("SupportedReasoningLevels = %#v, want %#v", model.SupportedReasoningLevels, want)
	}
	for i := range want {
		if model.SupportedReasoningLevels[i] != want[i] {
			t.Fatalf("SupportedReasoningLevels = %#v, want %#v", model.SupportedReasoningLevels, want)
		}
	}
}

// Rust #49345 (8ffd91e42a): the bundled Bedrock catalog keeps the Ultra
// reasoning level on every entry whose bundled model declares it (GPT-6
// 6.1-Sol/Astra/Sol and GPT-5.6 Sol/Terra in Go's fallback catalog) instead of
// stripping it as the pre-#49345 normalizer did.
func TestBedrockCatalogKeepsUltraReasoningLikeRust(t *testing.T) {
	catalog := AmazonBedrockModelCatalog()
	for _, model := range catalog.Models {
		hasUltra := false
		for _, level := range model.SupportedReasoningLevels {
			if level == "ultra" {
				hasUltra = true
			}
		}
		want := model.Slug == AmazonBedrockGPT61SolModelID || model.Slug == AmazonBedrockGPT6AstraModelID ||
			model.Slug == AmazonBedrockGPT6SolModelID ||
			model.Slug == AmazonBedrockGPT56SolModelID || model.Slug == AmazonBedrockGPT56TerraModelID
		if hasUltra != want {
			t.Fatalf("%s ultra = %v, want %v (levels %#v)", model.Slug, hasUltra, want, model.SupportedReasoningLevels)
		}
	}
}

func TestWithDefaultOnlyServiceTierClearsTiers(t *testing.T) {
	catalog := ModelsResponse{Models: []ModelInfo{{
		Slug:                 "gpt-5.5",
		AdditionalSpeedTiers: []string{"fast"},
		ServiceTiers:         []string{"default", "priority"},
		DefaultServiceTier:   "default",
	}}}
	updated := WithDefaultOnlyServiceTier(catalog)
	model := updated.Models[0]
	if len(model.AdditionalSpeedTiers) != 0 || len(model.ServiceTiers) != 0 || model.DefaultServiceTier != "" {
		t.Fatalf("model tiers = %#v", model)
	}
}

type recordingModelsEndpoint struct {
	calls     int
	etags     []string
	responses []*ModelsEndpointResponse
}

func (e *recordingModelsEndpoint) ListModels(_ context.Context, etag string) (*ModelsEndpointResponse, error) {
	e.calls++
	e.etags = append(e.etags, etag)
	if len(e.responses) == 0 {
		return &ModelsEndpointResponse{}, nil
	}
	response := e.responses[0]
	e.responses = e.responses[1:]
	return response, nil
}

func TestModelInfoAppsUsageDefaultsTrueAndPreservesSpecialty(t *testing.T) {
	var catalog ModelsResponse
	if err := json.Unmarshal([]byte(`{
		"models": [
			{"slug": "defaulted", "display_name": "Defaulted", "base_instructions": "base"},
			{"slug": "opted-out", "display_name": "Opted Out", "base_instructions": "base", "include_apps_usage_instructions": false, "model_specialty": "cyber"}
		]
	}`), &catalog); err != nil {
		t.Fatalf("Unmarshal catalog returned error: %v", err)
	}
	if len(catalog.Models) != 2 {
		t.Fatalf("models = %#v", catalog.Models)
	}
	if !catalog.Models[0].IncludeAppsUsageInstructions {
		t.Fatalf("missing include_apps_usage_instructions should default to true: %#v", catalog.Models[0])
	}
	if catalog.Models[1].IncludeAppsUsageInstructions {
		t.Fatalf("explicit opt-out should survive: %#v", catalog.Models[1])
	}
	if catalog.Models[1].ModelSpecialty != ModelSpecialtyCyber {
		t.Fatalf("model_specialty = %q, want %q", catalog.Models[1].ModelSpecialty, ModelSpecialtyCyber)
	}
	if catalog.Models[0].ModelSpecialty != "" {
		t.Fatalf("missing model_specialty should be empty: %#v", catalog.Models[0])
	}

	// Local fallback models mirror Rust's model_info_from_slug opt-out.
	if local := ModelInfoFromSlug("some-local-model"); local.IncludeAppsUsageInstructions {
		t.Fatalf("local fallback should opt out of apps usage instructions: %#v", local)
	}
}

func TestModelSummaryCarriesModelSpecialty(t *testing.T) {
	info := ModelInfo{Slug: "gpt-test", DisplayName: "GPT Test", BaseInstructions: "base", ModelSpecialty: ModelSpecialtyCyber}
	summary := summaryFromModel(info, false)
	if summary.ModelSpecialty == nil || *summary.ModelSpecialty != ModelSpecialtyCyber {
		t.Fatalf("modelSpecialty = %#v, want %q", summary.ModelSpecialty, ModelSpecialtyCyber)
	}
	encoded, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("Marshal error = %v", err)
	}
	if !strings.Contains(string(encoded), `"modelSpecialty":"cyber"`) {
		t.Fatalf("marshaled summary missing modelSpecialty: %s", encoded)
	}

	plain := summaryFromModel(ModelInfo{Slug: "gpt-plain", DisplayName: "GPT Plain", BaseInstructions: "base"}, false)
	if plain.ModelSpecialty != nil {
		t.Fatalf("empty specialty should stay nil: %#v", plain.ModelSpecialty)
	}
}

func TestModelInfoUpgradeRetirementTimeParsingMatchesRust(t *testing.T) {
	cases := []struct {
		name       string
		retirement any
		wantUnix   *int64
	}{
		{name: "absent", retirement: nil, wantUnix: nil},
		{name: "null", retirement: nil, wantUnix: nil},
		{name: "rfc3339", retirement: "2030-01-01T00:00:00Z", wantUnix: int64Ptr(1893456000)},
		{name: "malformed", retirement: "not-a-timestamp", wantUnix: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := map[string]any{"slug": "current-model", "display_name": "Current", "visibility": "list", "supported_in_api": true}
			upgrade := map[string]any{"model": "replacement-model", "migration_markdown": "Use the replacement model."}
			if tc.retirement != nil {
				upgrade["retirement_at"] = tc.retirement
			}
			if tc.name == "null" {
				upgrade["retirement_at"] = nil
			}
			payload["upgrade"] = upgrade
			data, err := json.Marshal(payload)
			if err != nil {
				t.Fatalf("Marshal error = %v", err)
			}
			var info ModelInfo
			if err := json.Unmarshal(data, &info); err != nil {
				t.Fatalf("Unmarshal error = %v", err)
			}
			if info.Upgrade == nil || info.Upgrade.Model != "replacement-model" {
				t.Fatalf("Upgrade = %#v, want replacement model", info.Upgrade)
			}
			if got := info.Upgrade.RetirementAt; !int64PtrEqual(got, tc.wantUnix) {
				t.Fatalf("RetirementAt = %v, want %v", int64PtrValue(got), int64PtrValue(tc.wantUnix))
			}
		})
	}
}

func TestModelSummaryCarriesUpgradeRetirementTime(t *testing.T) {
	retirement := int64(1893456000)
	info := ModelInfo{
		Slug:        "current-model",
		DisplayName: "Current",
		Upgrade: &ModelInfoUpgrade{
			Model:             "replacement-model",
			MigrationMarkdown: "Use the replacement model.",
			RetirementAt:      &retirement,
		},
	}
	summary := summaryFromModel(info, false)
	if summary.Upgrade == nil || *summary.Upgrade != "replacement-model" {
		t.Fatalf("summary.Upgrade = %#v, want replacement-model", summary.Upgrade)
	}
	if summary.UpgradeInfo == nil || summary.UpgradeInfo.Model != "replacement-model" || summary.UpgradeInfo.RetirementAt == nil || *summary.UpgradeInfo.RetirementAt != retirement {
		t.Fatalf("summary.UpgradeInfo = %#v, want retirement %d", summary.UpgradeInfo, retirement)
	}
	if summary.UpgradeInfo.MigrationMarkdown == nil || *summary.UpgradeInfo.MigrationMarkdown != "Use the replacement model." {
		t.Fatalf("summary.UpgradeInfo.MigrationMarkdown = %#v", summary.UpgradeInfo.MigrationMarkdown)
	}
}

func int64Ptr(value int64) *int64 {
	return &value
}

func int64PtrValue(value *int64) string {
	if value == nil {
		return "<nil>"
	}
	return strconv.FormatInt(*value, 10)
}

func int64PtrEqual(left *int64, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

// TestModelInfoUsableContextWindowLikeRust mirrors Rust #41162
// ModelInfo::usable_context_window: it reserves the effective context-window
// percent from the resolved context window, distinct from the auto-compaction
// limit.
func TestModelInfoUsableContextWindowLikeRust(t *testing.T) {
	model := ModelInfo{
		ContextWindow:                 272000,
		MaxContextWindow:              872000,
		AutoCompactTokenLimit:         250000,
		EffectiveContextWindowPercent: 95,
	}
	if usable, ok := model.usableContextWindow(); !ok || usable != 258400 {
		t.Fatalf("usableContextWindow = %d, %v, want 258400, true", usable, ok)
	}
	// Prefer the configured context_window over max_context_window.
	if window, ok := model.resolvedContextWindow(); !ok || window != 272000 {
		t.Fatalf("resolvedContextWindow = %d, %v, want 272000, true", window, ok)
	}
	// Default percent is 95 when unset.
	defaultModel := ModelInfo{ContextWindow: 100000}
	if usable, ok := defaultModel.usableContextWindow(); !ok || usable != 95000 {
		t.Fatalf("default usableContextWindow = %d, %v, want 95000, true", usable, ok)
	}
	// No context window yields none.
	if _, ok := (&ModelInfo{}).usableContextWindow(); ok {
		t.Fatalf("empty model usableContextWindow ok = true, want false")
	}
}

func TestModelInfoGuardianReviewPolicy(t *testing.T) {
	adaptive := GuardianReviewModeAdaptive
	synchronous := GuardianReviewModeSynchronous
	disabled := GuardianReviewModeDisabled

	policy := &GuardianModelPolicy{
		ComputerUse: &adaptive,
		Shell:       &synchronous,
	}

	if got := policy.ReviewMode(GuardianScopeComputerUse); got != adaptive {
		t.Fatalf("computer_use review mode = %q, want %q", got, adaptive)
	}
	if got := policy.ReviewMode(GuardianScopeShell); got != synchronous {
		t.Fatalf("shell review mode = %q, want %q", got, synchronous)
	}
	if got := policy.ReviewMode(GuardianScopeFileChanges); got != disabled {
		t.Fatalf("omitted file_changes review mode = %q, want %q", got, disabled)
	}
	if got := (*GuardianModelPolicy)(nil).ReviewMode(GuardianScopeShell); got != disabled {
		t.Fatalf("nil policy review mode = %q, want %q", got, disabled)
	}

	withPolicy := &ModelInfo{Guardian: policy}
	if !withPolicy.ComputerUseReviewRequired() {
		t.Fatal("adaptive computer_use policy should require review")
	}
	disabledPolicy := &ModelInfo{Guardian: &GuardianModelPolicy{ComputerUse: &disabled}}
	if disabledPolicy.ComputerUseReviewRequired() {
		t.Fatal("disabled computer_use policy should not require review")
	}
	omittedPolicy := &ModelInfo{Guardian: &GuardianModelPolicy{}}
	if omittedPolicy.ComputerUseReviewRequired() {
		t.Fatal("omitted computer_use policy should default to disabled (no review)")
	}

	legacy := &ModelInfo{NodeReplAutoReviewRequired: true}
	if !legacy.ComputerUseReviewRequired() {
		t.Fatal("legacy node_repl_auto_review_required should require review when no policy")
	}
	legacyOff := &ModelInfo{NodeReplAutoReviewRequired: false}
	if legacyOff.ComputerUseReviewRequired() {
		t.Fatal("legacy node_repl_auto_review_required=false should not require review")
	}
	if got := legacyOff.GuardianReviewMode(GuardianScopeComputerUse); got != nil {
		t.Fatalf("GuardianReviewMode without policy = %v, want nil", got)
	}
	if got := withPolicy.GuardianReviewMode(GuardianScopePermissions); got == nil || *got != disabled {
		t.Fatalf("GuardianReviewMode omitted permissions = %v, want disabled", got)
	}

	var parsed ModelInfo
	// Rust #45915 removed the code_mode scope, so an old `code_mode` key is
	// ignored like any other unknown scope while the remaining scopes parse.
	if err := json.Unmarshal([]byte(`{"guardian":{"computer_use":"adaptive","shell":"disabled","code_mode":"future_mode","permissions":"adaptive"}}`), &parsed); err != nil {
		t.Fatalf("unmarshal guardian policy: %v", err)
	}
	if parsed.Guardian == nil || parsed.Guardian.ComputerUse == nil || *parsed.Guardian.ComputerUse != adaptive {
		t.Fatalf("parsed guardian computer_use = %#v, want adaptive", parsed.Guardian)
	}
	if parsed.Guardian.Permissions == nil || *parsed.Guardian.Permissions != adaptive {
		t.Fatalf("parsed guardian permissions = %#v, want adaptive", parsed.Guardian)
	}
}

// TestModelInfoFromSlugMatchesRustFallback pins the fallback descriptor against
// Rust models_manager::model_info_from_slug: unified exec, no skills/apps/plugin
// usage instructions, reasoning summaries supported, text web search, a
// 10k-byte truncation policy and the 272k context window, all flagged as
// fallback metadata.
func TestModelInfoFromSlugMatchesRustFallback(t *testing.T) {
	info := ModelInfoFromSlug("gpt-future")
	if info.Slug != "gpt-future" || info.DisplayName != "gpt-future" {
		t.Fatalf("slug/display = %q/%q", info.Slug, info.DisplayName)
	}
	if info.ShellType != "unified_exec" {
		t.Fatalf("shell type = %q, want unified_exec", info.ShellType)
	}
	if info.Visibility != VisibilityNone || !info.SupportedInAPI || info.Priority != 99 {
		t.Fatalf("visibility/api/priority = %q/%v/%d", info.Visibility, info.SupportedInAPI, info.Priority)
	}
	if info.IncludeSkillsUsageInstructions || info.IncludeAppsUsageInstructions || info.IncludePluginUsageInstructions {
		t.Fatalf("usage instructions = skills:%v apps:%v plugins:%v",
			info.IncludeSkillsUsageInstructions, info.IncludeAppsUsageInstructions, info.IncludePluginUsageInstructions)
	}
	if !info.SupportsReasoningSummaries || info.DefaultReasoningSummary != "auto" {
		t.Fatalf("reasoning summaries = %v/%q", info.SupportsReasoningSummaries, info.DefaultReasoningSummary)
	}
	if info.SupportVerbosity || info.SupportsImageDetailOriginal || info.SupportsSearchTool || info.SupportsExperimentalContext || info.UseResponsesLite {
		t.Fatalf("capability flags = %+v", info)
	}
	if info.WebSearchToolType != "text" {
		t.Fatalf("web search tool type = %q", info.WebSearchToolType)
	}
	if info.TruncationPolicy.Mode != TruncationModeBytes || info.TruncationPolicy.Limit != 10000 {
		t.Fatalf("truncation policy = %+v", info.TruncationPolicy)
	}
	if info.ContextWindow != 272000 || info.MaxContextWindow != 272000 || info.EffectiveContextWindowPercent != 95 {
		t.Fatalf("context window = %d/%d/%d", info.ContextWindow, info.MaxContextWindow, info.EffectiveContextWindowPercent)
	}
	if !info.UsedFallbackModelMetadata {
		t.Fatal("fallback metadata flag is not set")
	}
	if info.ModelMessages == nil || info.ModelMessages.InstructionsTemplate == "" {
		t.Fatalf("model messages = %#v", info.ModelMessages)
	}
}

// Mirrors Rust #49339 (a6e9eaa9bd) `catalog_uses_mantle_model_ids_in_priority_order`
// together with `bedrock_models_preserve_source_metadata_with_supported_capabilities`
// and `gpt_5_bedrock_models_use_bedrock_context_window` in
// codex-rs/model-provider/src/amazon_bedrock/catalog.rs. The GPT-6 Sol/Luna
// entries come from Rust #47347 (df30941072), which the Go catalog had not
// backfilled before this change.
func TestAmazonBedrockModelCatalogLikeRust(t *testing.T) {
	catalog := AmazonBedrockModelCatalog()

	wantMetadata := []struct {
		slug        string
		displayName string
		priority    int
	}{
		{AmazonBedrockGPT61SolModelID, "GPT-6.1 Sol", 0},
		{AmazonBedrockGPT6AstraModelID, "GPT-6-Astra", 1},
		{AmazonBedrockGPT6SolModelID, "GPT-6 Sol", 2},
		{AmazonBedrockGPT6LunaModelID, "GPT-6 Luna", 3},
		{AmazonBedrockGPT56SolModelID, "GPT-5.6 Sol", 4},
		{AmazonBedrockGPT56TerraModelID, "GPT-5.6 Terra", 5},
		{AmazonBedrockGPT56LunaModelID, "GPT-5.6 Luna", 6},
		{AmazonBedrockGPT55ModelID, "GPT-5.5", 7},
	}
	if len(catalog.Models) != len(wantMetadata) {
		t.Fatalf("bedrock catalog len = %d, want %d", len(catalog.Models), len(wantMetadata))
	}
	for i, want := range wantMetadata {
		model := catalog.Models[i]
		if model.Slug != want.slug || model.DisplayName != want.displayName || model.Priority != want.priority {
			t.Fatalf("model[%d] = (%s, %s, %d), want (%s, %s, %d)",
				i, model.Slug, model.DisplayName, model.Priority, want.slug, want.displayName, want.priority)
		}
		if model.Visibility != VisibilityList {
			t.Fatalf("%s visibility = %q, want list", model.Slug, model.Visibility)
		}
		if len(model.AdditionalSpeedTiers) != 0 || model.DefaultServiceTier != "" {
			t.Fatalf("%s speed tiers = %#v default = %q", model.Slug, model.AdditionalSpeedTiers, model.DefaultServiceTier)
		}
		if model.WebSearchToolType != "text" {
			t.Fatalf("%s WebSearchToolType = %q, want text", model.Slug, model.WebSearchToolType)
		}
		if model.ContextWindow != 272000 {
			t.Fatalf("%s ContextWindow = %d, want 272000", model.Slug, model.ContextWindow)
		}
		wantMax := int64(272000)
		switch model.Slug {
		case AmazonBedrockGPT61SolModelID, AmazonBedrockGPT6AstraModelID, AmazonBedrockGPT6SolModelID,
			AmazonBedrockGPT6LunaModelID, AmazonBedrockGPT56SolModelID, AmazonBedrockGPT56TerraModelID,
			AmazonBedrockGPT56LunaModelID:
			wantMax = 872000
		}
		if model.MaxContextWindow != wantMax {
			t.Fatalf("%s MaxContextWindow = %d, want %d", model.Slug, model.MaxContextWindow, wantMax)
		}
	}
}

// Mirrors Rust #50472 (604061ce51)
// `bedrock_models_preserve_source_metadata_with_supported_capabilities` and
// `bedrock_models_do_not_enable_priority_or_explicit_default_tiers` in
// codex-rs/model-provider/src/amazon_bedrock/catalog.rs: the bundled Bedrock
// Mantle catalog clears every inherited speed/service tier and the default
// tier, except that the GPT-6 Astra entry advertises `ultrafast`.
func TestBedrockCatalogAdvertisesUltrafastForAstraLikeRust(t *testing.T) {
	catalog := AmazonBedrockModelCatalog()
	for _, model := range catalog.Models {
		if model.DefaultServiceTier != "" {
			t.Fatalf("%s DefaultServiceTier = %q, want empty", model.Slug, model.DefaultServiceTier)
		}
		if len(model.AdditionalSpeedTiers) != 0 {
			t.Fatalf("%s AdditionalSpeedTiers = %#v, want none", model.Slug, model.AdditionalSpeedTiers)
		}
		want := []string(nil)
		if model.Slug == AmazonBedrockGPT6AstraModelID {
			want = []string{"ultrafast"}
		}
		if len(model.ServiceTiers) != len(want) {
			t.Fatalf("%s ServiceTiers = %#v, want %#v", model.Slug, model.ServiceTiers, want)
		}
		for i := range want {
			if model.ServiceTiers[i] != want[i] {
				t.Fatalf("%s ServiceTiers = %#v, want %#v", model.Slug, model.ServiceTiers, want)
			}
		}
	}
}

// Mirrors Rust #38470 (d5e256ceb2)
// `runtime_catalog_includes_supported_cross_region_models_in_priority_order` in
// codex-rs/model-provider/src/amazon_bedrock/runtime_catalog_tests.rs, at the
// shape upstream has after #38617 (global-first grouping), #42619 (GPT-6-Astra),
// #47347 (GPT-6 Sol/Luna), #49339 (GPT-6.1 Sol) and #50472 (ultrafast tiers)
// extended the Runtime variant set to every Mantle model except GPT-5.5 (see
// runtime_catalog.rs at upstream 5a3140176e). Asserts the whole catalog, not
// just the newly added entries.
func TestAmazonBedrockRuntimeModelCatalogLikeRust(t *testing.T) {
	catalog := AmazonBedrockRuntimeModelCatalog()

	want := []struct {
		slug        string
		displayName string
		priority    int
	}{
		{bedrockRuntimeGlobalSlugPrefix + AmazonBedrockGPT61SolModelID, "GPT-6.1 Sol (Global)", 0},
		{bedrockRuntimeGlobalSlugPrefix + AmazonBedrockGPT6AstraModelID, "GPT-6-Astra (Global)", 1},
		{bedrockRuntimeGlobalSlugPrefix + AmazonBedrockGPT6SolModelID, "GPT-6 Sol (Global)", 2},
		{bedrockRuntimeGlobalSlugPrefix + AmazonBedrockGPT6LunaModelID, "GPT-6 Luna (Global)", 3},
		{bedrockRuntimeGlobalSlugPrefix + AmazonBedrockGPT56SolModelID, "GPT-5.6 Sol (Global)", 4},
		{bedrockRuntimeGlobalSlugPrefix + AmazonBedrockGPT56TerraModelID, "GPT-5.6 Terra (Global)", 5},
		{bedrockRuntimeGlobalSlugPrefix + AmazonBedrockGPT56LunaModelID, "GPT-5.6 Luna (Global)", 6},
		{bedrockRuntimeUSSlugPrefix + AmazonBedrockGPT61SolModelID, "GPT-6.1 Sol (US cross-region)", 7},
		{bedrockRuntimeUSSlugPrefix + AmazonBedrockGPT6AstraModelID, "GPT-6-Astra (US cross-region)", 8},
		{bedrockRuntimeUSSlugPrefix + AmazonBedrockGPT6SolModelID, "GPT-6 Sol (US cross-region)", 9},
		{bedrockRuntimeUSSlugPrefix + AmazonBedrockGPT6LunaModelID, "GPT-6 Luna (US cross-region)", 10},
		{bedrockRuntimeUSSlugPrefix + AmazonBedrockGPT56SolModelID, "GPT-5.6 Sol (US cross-region)", 11},
		{bedrockRuntimeUSSlugPrefix + AmazonBedrockGPT56TerraModelID, "GPT-5.6 Terra (US cross-region)", 12},
		{bedrockRuntimeUSSlugPrefix + AmazonBedrockGPT56LunaModelID, "GPT-5.6 Luna (US cross-region)", 13},
	}
	if len(catalog.Models) != len(want) {
		t.Fatalf("runtime catalog len = %d, want %d", len(catalog.Models), len(want))
	}
	for i, expected := range want {
		model := catalog.Models[i]
		if model.Slug != expected.slug || model.DisplayName != expected.displayName || model.Priority != expected.priority {
			t.Fatalf("model[%d] = (%s, %s, %d), want (%s, %s, %d)",
				i, model.Slug, model.DisplayName, model.Priority, expected.slug, expected.displayName, expected.priority)
		}
		if model.Visibility != VisibilityList {
			t.Fatalf("%s visibility = %q, want list", model.Slug, model.Visibility)
		}
		// The Runtime variants inherit the Mantle base metadata: GPT-5.5 has no
		// cross-region variant, so every entry keeps the long-context window.
		if model.ContextWindow != 272000 || model.MaxContextWindow != 872000 {
			t.Fatalf("%s context window = %d/%d, want 272000/872000", model.Slug, model.ContextWindow, model.MaxContextWindow)
		}
		if model.WebSearchToolType != "text" {
			t.Fatalf("%s WebSearchToolType = %q, want text", model.Slug, model.WebSearchToolType)
		}
		// Rust #50472 advertises `ultrafast` for the Runtime Astra variants only.
		wantTiers := []string(nil)
		switch model.Slug {
		case bedrockRuntimeGlobalSlugPrefix + AmazonBedrockGPT6AstraModelID,
			bedrockRuntimeUSSlugPrefix + AmazonBedrockGPT6AstraModelID:
			wantTiers = []string{bedrockUltrafastServiceTierID}
		}
		if len(model.ServiceTiers) != len(wantTiers) {
			t.Fatalf("%s ServiceTiers = %#v, want %#v", model.Slug, model.ServiceTiers, wantTiers)
		}
		for tierIndex := range wantTiers {
			if model.ServiceTiers[tierIndex] != wantTiers[tierIndex] {
				t.Fatalf("%s ServiceTiers = %#v, want %#v", model.Slug, model.ServiceTiers, wantTiers)
			}
		}
		if len(model.AdditionalSpeedTiers) != 0 || model.DefaultServiceTier != "" {
			t.Fatalf("%s speed tiers = %#v default = %q, want none", model.Slug, model.AdditionalSpeedTiers, model.DefaultServiceTier)
		}
	}
	for _, model := range catalog.Models {
		if model.Slug == AmazonBedrockGPT55ModelID ||
			strings.TrimSuffix(strings.TrimPrefix(model.Slug, bedrockRuntimeGlobalSlugPrefix), bedrockRuntimeUSSlugPrefix) == AmazonBedrockGPT55ModelID {
			t.Fatalf("runtime catalog unexpectedly contains a GPT-5.5 variant: %s", model.Slug)
		}
	}
}

// Mirrors Rust #38470 (d5e256ceb2)
// `runtime_catalog_disables_web_search_without_overriding_review_models` in
// codex-rs/model-provider/src/amazon_bedrock/runtime_catalog_tests.rs: every
// Runtime variant clears `supports_search_tool` (the Runtime endpoint cannot
// host web search), leaves `auto_review_model_override` unset, and — since
// Rust #49345 (8ffd91e42a) — keeps the `multi_agent_version` of its base model
// instead of forcing multi-agent V1 (the pre-#49345 normalizer did the latter).
func TestAmazonBedrockRuntimeCatalogDisablesWebSearchLikeRust(t *testing.T) {
	baseVersions := map[string]string{}
	for _, model := range AmazonBedrockModelCatalog().Models {
		baseVersions[model.Slug] = model.MultiAgentVersion
	}
	catalog := AmazonBedrockRuntimeModelCatalog()
	if len(catalog.Models) == 0 {
		t.Fatal("runtime catalog is empty")
	}
	for _, model := range catalog.Models {
		if model.SupportsSearchTool {
			t.Fatalf("%s SupportsSearchTool = true, want false", model.Slug)
		}
		if model.AutoReviewModelOverride != "" {
			t.Fatalf("%s AutoReviewModelOverride = %q, want empty", model.Slug, model.AutoReviewModelOverride)
		}
		base := strings.TrimPrefix(strings.TrimPrefix(model.Slug, bedrockRuntimeGlobalSlugPrefix), bedrockRuntimeUSSlugPrefix)
		if want := baseVersions[base]; model.MultiAgentVersion != want {
			t.Fatalf("%s MultiAgentVersion = %q, want %q (preserved from %s)", model.Slug, model.MultiAgentVersion, want, base)
		}
		// Concrete expectations for the models Go's bundled catalog carries,
		// matching Rust's pinned runtime table.
		wantVersion := ""
		switch base {
		case AmazonBedrockGPT61SolModelID, AmazonBedrockGPT6AstraModelID,
			AmazonBedrockGPT6SolModelID, AmazonBedrockGPT6LunaModelID,
			AmazonBedrockGPT56SolModelID, AmazonBedrockGPT56TerraModelID:
			wantVersion = "v2"
		case AmazonBedrockGPT56LunaModelID:
			wantVersion = "v1"
		}
		if model.MultiAgentVersion != wantVersion {
			t.Fatalf("%s MultiAgentVersion = %q, want %q", model.Slug, model.MultiAgentVersion, wantVersion)
		}
	}
}

// Mirrors Rust AmazonBedrockModelProvider::default_model_catalog
// (codex-rs/model-provider/src/amazon_bedrock/mod.rs, #38470): the Mantle
// provider keeps the shared Bedrock catalog while `amazon-bedrock-runtime`
// resolves to the cross-region catalog.
func TestAmazonBedrockCatalogForProviderIDLikeRust(t *testing.T) {
	mantle := AmazonBedrockCatalogForProviderID(AmazonBedrockProviderID)
	if len(mantle.Models) != len(AmazonBedrockModelCatalog().Models) || mantle.Models[0].Slug != AmazonBedrockGPT61SolModelID {
		t.Fatalf("mantle catalog = %#v", mantle.Models)
	}
	runtimeCatalog := AmazonBedrockCatalogForProviderID(AmazonBedrockRuntimeProviderID)
	if len(runtimeCatalog.Models) != len(AmazonBedrockRuntimeModelCatalog().Models) ||
		runtimeCatalog.Models[0].Slug != bedrockRuntimeGlobalSlugPrefix+AmazonBedrockGPT61SolModelID {
		t.Fatalf("runtime catalog = %#v", runtimeCatalog.Models)
	}
}

// TestBundledFallbackCarriesCatalogJSONBooleans pins the two
// #[serde(default = "default_true")] booleans
// (codex-rs/protocol/src/openai_models.rs:438/441) of the hand-written
// fallback catalog against the bundled catalog JSON
// (codex-rs/models-manager/models.json @ b17c74cfd5) read through the ordinary
// catalog parse path: the value of a field must not depend on which of the two
// sources produced the entry. The fallback literals bypass
// ModelInfo.UnmarshalJSON, where those defaults live (catalog.go:712/714), so
// before the fix supports_reasoning_summary_parameter read false on the
// fallback path and true on the models.json path for the same slug.
func TestBundledFallbackCarriesCatalogJSONBooleans(t *testing.T) {
	// Verbatim from codex-rs/models-manager/models.json @ b17c74cfd5, restricted
	// to the entries the Go fallback catalog mirrors.
	const catalogJSON = `{"models":[
		{"slug":"gpt-6.1-sol","supports_reasoning_summary_parameter":true,"include_apps_usage_instructions":false},
		{"slug":"gpt-6-astra","supports_reasoning_summary_parameter":true,"include_apps_usage_instructions":false},
		{"slug":"gpt-6-sol","supports_reasoning_summary_parameter":true,"include_apps_usage_instructions":false},
		{"slug":"gpt-6-luna","supports_reasoning_summary_parameter":true,"include_apps_usage_instructions":false},
		{"slug":"gpt-5.6-sol","supports_reasoning_summary_parameter":true,"include_apps_usage_instructions":true},
		{"slug":"gpt-5.6-terra","supports_reasoning_summary_parameter":true,"include_apps_usage_instructions":true},
		{"slug":"gpt-5.6-luna","supports_reasoning_summary_parameter":true,"include_apps_usage_instructions":true},
		{"slug":"gpt-5.5","supports_reasoning_summary_parameter":true,"include_apps_usage_instructions":true},
		{"slug":"codex-auto-review","supports_reasoning_summary_parameter":true,"include_apps_usage_instructions":false}
	]}`
	var fromCatalogJSON ModelsResponse
	if err := json.Unmarshal([]byte(catalogJSON), &fromCatalogJSON); err != nil {
		t.Fatalf("unmarshal models.json subset: %v", err)
	}
	bySlug := make(map[string]ModelInfo, len(fromCatalogJSON.Models))
	for _, model := range fromCatalogJSON.Models {
		bySlug[model.Slug] = model
	}
	// Slugs the catalog JSON no longer carries resolve through Rust's
	// model_info_from_slug descriptor instead.
	goOnly := map[string]bool{"gpt-5.2": true, "gpt-5.4-mini": true}

	fallback := fallbackBundledModelsResponse()
	if len(fallback.Models) == 0 {
		t.Fatal("fallback catalog is empty")
	}
	for _, model := range fallback.Models {
		source, ok := bySlug[model.Slug]
		if !ok {
			if !goOnly[model.Slug] {
				t.Fatalf("fallback slug %q has no models.json counterpart", model.Slug)
			}
			source = ModelInfoFromSlug(model.Slug)
		}
		if model.SupportsReasoningSummaries != source.SupportsReasoningSummaries {
			t.Fatalf("%s: supports_reasoning_summary_parameter = %v on the fallback path, %v on the models.json path",
				model.Slug, model.SupportsReasoningSummaries, source.SupportsReasoningSummaries)
		}
		if model.IncludeAppsUsageInstructions != source.IncludeAppsUsageInstructions {
			t.Fatalf("%s: include_apps_usage_instructions = %v on the fallback path, %v on the models.json path",
				model.Slug, model.IncludeAppsUsageInstructions, source.IncludeAppsUsageInstructions)
		}
	}
}

// TestFallbackCatalogCarriesModelsJSONFieldValues pins the fallback catalog
// entries that used to carry values different from the bundled catalog JSON
// (codex-rs/models-manager/models.json @ b17c74cfd5): the two explicit
// truncation/context-window errors and the stale picker copy of the 5.6 family
// and gpt-5.5. Every assertion is the Rust value, so an edit that silently
// reverts one of them fails here instead of only changing a number.
func TestFallbackCatalogCarriesModelsJSONFieldValues(t *testing.T) {
	catalog := fallbackBundledModelsResponse()
	bySlug := make(map[string]ModelInfo, len(catalog.Models))
	for _, model := range catalog.Models {
		bySlug[model.Slug] = model
	}

	// models.json @ b17c74cfd5 descriptions (Rust renamed the 5.6 family to
	// "Older ..." and gpt-5.5 to "Legacy ...").
	wantDescription := []struct{ slug, want string }{
		{"gpt-5.6-sol", "Older generation workhorse model."},
		{"gpt-5.6-terra", "Older balanced model for straightforward work."},
		{"gpt-5.6-luna", "Older fast and efficient model."},
		{"gpt-5.5", "Legacy coding model."},
	}
	for _, testCase := range wantDescription {
		slug, want := testCase.slug, testCase.want
		model, ok := bySlug[slug]
		if !ok {
			t.Fatalf("fallback catalog lost %q", slug)
		}
		if model.Description != want {
			t.Fatalf("%s description = %q, want %q (models.json @ b17c74cfd5)", slug, model.Description, want)
		}
	}

	// models.json truncations by tokens, not bytes.
	for _, slug := range []string{"gpt-5.5", "codex-auto-review"} {
		model, ok := bySlug[slug]
		if !ok {
			t.Fatalf("fallback catalog lost %q", slug)
		}
		if model.TruncationPolicy.Mode != TruncationModeTokens || model.TruncationPolicy.Limit != 10000 {
			t.Fatalf("%s truncation_policy = %+v, want {mode:tokens limit:10000}", slug, model.TruncationPolicy)
		}
	}

	// models.json gives codex-auto-review the shared 872,000 token maximum; Go
	// carried the legacy 1,000,000 value, which is the review model's context
	// budget.
	review, ok := bySlug["codex-auto-review"]
	if !ok {
		t.Fatal("fallback catalog lost codex-auto-review")
	}
	if review.MaxContextWindow != 872000 {
		t.Fatalf("codex-auto-review max_context_window = %d, want 872000 (models.json @ b17c74cfd5)", review.MaxContextWindow)
	}
	if review.ContextWindow != 272000 {
		t.Fatalf("codex-auto-review context_window = %d, want 272000", review.ContextWindow)
	}
}
