package exec

import (
	"strings"

	"codex_go/auth"
	"codex_go/codexapi"
	"codex_go/config"
	"codex_go/features"
	"codex_go/model"
	"codex_go/turn"
)

func (r *Runner) webSearchOptionsForRun(
	cfg *config.Config,
	resolvedAuth *auth.ResolvedAuth,
	providerID string,
	modelID string,
	threadID string,
	inputItems []any,
	forceLive bool,
	permissions *config.SandboxPermissionProfileResolution,
) (*turn.WebSearchOptions, error) {
	if r == nil || cfg == nil || !r.UseResponsesAPI {
		return nil, nil
	}
	mode := execWebSearchMode(cfg, forceLive)
	if mode == codexapi.WebSearchModeDisabled {
		return nil, nil
	}
	providerInfo, err := model.ProviderForConfigID(configValues(cfg), providerID, stringConfigValue(cfg, "openai_base_url"))
	if err != nil {
		return nil, err
	}
	if !providerInfo.IsOpenAI() && !providerInfo.UsesOpenAIActorAuthorization() && !providerInfo.SupportsStandaloneWebSearch {
		return nil, nil
	}
	var snapshot *auth.AuthDotJSON
	if resolvedAuth != nil {
		snapshot = &resolvedAuth.Auth
	}
	if providerInfo.RequiresOpenAIAuth && snapshot == nil {
		return nil, nil
	}
	runtimeProvider := model.CreateRuntimeProviderForID(providerID, *providerInfo, snapshot)
	modelInfo := execModelInfo(modelID, cfg)
	capabilities := runtimeProvider.Capabilities()
	// Rust #62894: the effective mode follows the run's permission profile and
	// the provider capabilities.
	mode = execWebSearchModeForTurn(cfg, forceLive, permissions, capabilities.ExternalWebAccess)
	if mode == codexapi.WebSearchModeDisabled {
		return nil, nil
	}
	standaloneEnabled := capabilities.WebSearch &&
		(modelInfo.UseResponsesLite || features.Enabled(cfg.FeatureSettings(), "standalone_web_search"))
	if !standaloneEnabled {
		return nil, nil
	}
	apiProvider, authHeaders, err := runtimeProvider.APIProviderAndAuth()
	if err != nil {
		return nil, err
	}
	return &turn.WebSearchOptions{
		SessionID:                strings.TrimSpace(threadID),
		Model:                    strings.TrimSpace(modelID),
		Provider:                 apiProvider,
		Auth:                     authHeaders,
		HTTPClient:               r.httpClientForConfig(cfg),
		InputItems:               append([]any(nil), inputItems...),
		Settings:                 codexapi.SearchSettingsForMode(mode, execWebSearchToolConfig(cfg)),
		MaxOutputTokens:          turn.WebSearchMaxOutputTokens(&modelInfo, cfg.ToolOutputTokenLimit()),
		SupportsLiteratureSearch: execModelIncludesExperimentalTool(modelInfo, turn.LiteratureSearchQueryToolName),
	}, nil
}

func execModelIncludesExperimentalTool(modelInfo model.ModelInfo, name string) bool {
	for _, supported := range modelInfo.ExperimentalSupportedTools {
		if strings.TrimSpace(supported) == strings.TrimSpace(name) {
			return true
		}
	}
	return false
}

func execWebSearchMode(cfg *config.Config, forceLive bool) codexapi.WebSearchMode {
	if forceLive {
		return codexapi.WebSearchModeLive
	}
	if cfg == nil {
		return codexapi.WebSearchModeCached
	}
	return cfg.WebSearchModePreference()
}

// execWebSearchModeForTurn mirrors Rust resolve_web_search_mode_for_turn for the
// exec run's resolved permission profile (Rust #62894).
func execWebSearchModeForTurn(cfg *config.Config, forceLive bool, permissions *config.SandboxPermissionProfileResolution, externalWebAccess bool) codexapi.WebSearchMode {
	if cfg == nil {
		return execWebSearchMode(cfg, forceLive)
	}
	fullAccess := permissions != nil && permissions.Profile != nil && permissions.Profile.Disabled
	wasExplicit := forceLive || cfg.WebSearchModeWasExplicit()
	return config.ResolveWebSearchModeForTurn(
		execWebSearchMode(cfg, forceLive),
		wasExplicit,
		fullAccess,
		cfg.AllowedWebSearchModes(),
		externalWebAccess,
	)
}

func execWebSearchToolConfig(cfg *config.Config) map[string]any {
	if cfg == nil || cfg.Values == nil {
		return nil
	}
	tools, ok := cfg.Values["tools"].(map[string]any)
	if !ok {
		return nil
	}
	webSearch, _ := tools["web_search"].(map[string]any)
	return webSearch
}
