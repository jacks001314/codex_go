package appserver

import (
	"strings"

	"codex_go/auth"
	"codex_go/codexapi"
	"codex_go/config"
	"codex_go/features"
	"codex_go/model"
	"codex_go/turn"
)

func (r *RuntimeRouter) webSearchOptionsForTurn(cfg *config.Config, params *turn.TurnStartParams) (*turn.WebSearchOptions, error) {
	mode := webSearchModeFromConfig(cfg)
	if cfg == nil || turnStartReviewRuntime(params) || mode == codexapi.WebSearchModeDisabled {
		return nil, nil
	}
	modelProviderConfig, err := r.appTurnModelProviderConfig(cfg, params)
	if err != nil {
		return nil, err
	}
	providerInfo, err := model.ProviderForConfigID(configValues(cfg), modelProviderConfig.ProviderID, stringConfigValue(cfg, "openai_base_url"))
	if err != nil {
		return nil, err
	}
	if !providerInfo.IsOpenAI() && !providerInfo.UsesOpenAIActorAuthorization() && !providerInfo.SupportsStandaloneWebSearch {
		return nil, nil
	}
	var snapshot *auth.AuthDotJSON
	if providerInfo.RequiresOpenAIAuth {
		resolved, err := r.resolveAuthWithLoginRestrictions(r.codexHomeForRollout())
		if err != nil {
			return nil, err
		}
		if resolved == nil {
			return nil, nil
		}
		snapshot = &resolved.Auth
	}
	runtimeProvider := model.CreateRuntimeProviderForID(modelProviderConfig.ProviderID, *providerInfo, snapshot)
	modelInfo := r.modelInfoForRuntimeWithConfig(modelProviderConfig.Model, cfg)
	if !appStandaloneWebSearchEnabled(runtimeProvider.Capabilities(), modelInfo, cfg.FeatureSettings()) {
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
	threadID := ""
	if params != nil {
		threadID = strings.TrimSpace(params.ThreadID)
	}
	metadata := turn.BuildResponsesClientMetadata(&turn.ResponsesClientMetadataOptions{
		SessionID:                  threadID,
		ThreadID:                   threadID,
		RequestKind:                codexapi.ClientRequestTurn,
		NodeReplAutoReviewRequired: &modelInfo.NodeReplAutoReviewRequired,
		NodeReplDisabled:           &modelInfo.NodeReplDisabled,
		Extra:                      turn.MergeClientMetadata(cfg.ResponsesAPIClientMetadata(), responsesMetadataFromTurnStart(params)),
		ResponsesAPIMetadata:       cfg.ResponsesAPIMetadata(),
	})
	return &turn.WebSearchOptions{
		SessionID:       threadID,
		Model:           modelProviderConfig.Model,
		Originator:      turnOriginator(params),
		TurnMetadata:    metadata[codexapi.ClientCodexTurnMetadataHeader],
		Provider:        apiProvider,
		Auth:            authHeaders,
		HTTPClient:      r.httpClientForConfig(cfg),
		InputItems:      r.webSearchInputItemsForTurn(threadID, params),
		Settings:        webSearchSettingsFromConfig(cfg, mode),
		MaxOutputTokens: turn.WebSearchMaxOutputTokens(modelInfo, cfg.ToolOutputTokenLimit()),
	}, nil
}

func appStandaloneWebSearchEnabled(capabilities model.ProviderCapabilities, info *model.ModelInfo, featureSettings map[string]bool) bool {
	if info == nil || !capabilities.NamespaceTools || !capabilities.WebSearch {
		return false
	}
	return info.UseResponsesLite || features.Enabled(featureSettings, "standalone_web_search")
}

func (r *RuntimeRouter) webSearchInputItemsForTurn(threadID string, params *turn.TurnStartParams) []any {
	historyItems, _ := r.historyInputItemsForTurn(threadID)
	items := append([]any(nil), historyItems...)
	if current := model.UserMessageInputItem(promptFromTurnStart(params)); current != nil {
		items = append(items, current)
	}
	return items
}

func webSearchSettingsFromConfig(cfg *config.Config, mode codexapi.WebSearchMode) *codexapi.SearchSettings {
	return codexapi.SearchSettingsForMode(mode, webSearchToolConfig(cfg))
}

func webSearchModeFromConfig(cfg *config.Config) codexapi.WebSearchMode {
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

func responsesMetadataFromTurnStart(params *turn.TurnStartParams) map[string]string {
	if params == nil {
		return nil
	}
	return params.ResponsesAPIMetadata
}

func webSearchToolConfig(cfg *config.Config) map[string]any {
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

func locationFromWebSearchToolConfig(webSearch map[string]any) *codexapi.ApproximateLocation {
	location, ok := webSearch["location"].(map[string]any)
	if !ok || len(location) == 0 {
		return nil
	}
	country := stringPtrIfNotEmpty(stringFromMapAny(location, "country"))
	region := stringPtrIfNotEmpty(stringFromMapAny(location, "region"))
	city := stringPtrIfNotEmpty(stringFromMapAny(location, "city"))
	timezone := stringPtrIfNotEmpty(stringFromMapAny(location, "timezone"))
	if country == nil && region == nil && city == nil && timezone == nil {
		return nil
	}
	return &codexapi.ApproximateLocation{
		Type:     codexapi.LocationApproximate,
		Country:  country,
		Region:   region,
		City:     city,
		Timezone: timezone,
	}
}

func stringFromMapAny(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func stringSliceFromMapAny(values map[string]any, keys ...string) []string {
	for _, key := range keys {
		value, ok := values[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case []string:
			out := make([]string, 0, len(typed))
			for _, item := range typed {
				if item = strings.TrimSpace(item); item != "" {
					out = append(out, item)
				}
			}
			return out
		case []any:
			out := make([]string, 0, len(typed))
			for _, item := range typed {
				if text, ok := item.(string); ok {
					if text = strings.TrimSpace(text); text != "" {
						out = append(out, text)
					}
				}
			}
			return out
		}
	}
	return nil
}
