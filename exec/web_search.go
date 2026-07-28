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
	standaloneEnabled := capabilities.NamespaceTools && capabilities.WebSearch &&
		(modelInfo.UseResponsesLite || features.Enabled(cfg.FeatureSettings(), "standalone_web_search"))
	if !standaloneEnabled {
		return nil, nil
	}
	apiProvider, err := runtimeProvider.APIProvider()
	if err != nil {
		return nil, err
	}
	authHeaders, err := runtimeProvider.APIAuth()
	if err != nil {
		return nil, err
	}
	return &turn.WebSearchOptions{
		SessionID:       strings.TrimSpace(threadID),
		Model:           strings.TrimSpace(modelID),
		Provider:        apiProvider,
		Auth:            authHeaders,
		HTTPClient:      r.httpClientForConfig(cfg),
		InputItems:      append([]any(nil), inputItems...),
		Settings:        codexapi.SearchSettingsForMode(mode, execWebSearchToolConfig(cfg)),
		MaxOutputTokens: turn.WebSearchMaxOutputTokens(&modelInfo, cfg.ToolOutputTokenLimit()),
	}, nil
}

func execWebSearchMode(cfg *config.Config, forceLive bool) codexapi.WebSearchMode {
	if forceLive {
		return codexapi.WebSearchModeLive
	}
	if cfg == nil || cfg.Values == nil {
		return codexapi.WebSearchModeCached
	}
	if value, ok := cfg.Values["web_search"]; ok {
		return codexapi.WebSearchModeFromValue(value)
	}
	featureSettings := cfg.FeatureSettings()
	if features.Enabled(featureSettings, "web_search_cached") {
		return codexapi.WebSearchModeCached
	}
	if features.Enabled(featureSettings, "web_search_request") {
		return codexapi.WebSearchModeLive
	}
	return codexapi.WebSearchModeCached
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
