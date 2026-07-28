package appserver

import (
	"strings"

	"codex_go/auth"
	"codex_go/config"
	"codex_go/features"
	"codex_go/model"
	"codex_go/turn"
)

func (r *RuntimeRouter) imageGenerationOptionsForTurn(cfg *config.Config, params *turn.TurnStartParams) (*turn.ImageGenerationOptions, error) {
	if cfg == nil || turnStartReviewRuntime(params) {
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
	} else if resolved, err := r.resolveAuthWithLoginRestrictions(r.codexHomeForRollout()); err == nil && resolved != nil {
		snapshot = &resolved.Auth
	}
	runtimeProvider := model.CreateRuntimeProviderForID(modelProviderConfig.ProviderID, *providerInfo, snapshot)
	modelInfo := r.modelInfoForRuntimeWithConfig(modelProviderConfig.Model, cfg)
	if !appImageGenerationStandaloneEnabled(*providerInfo, runtimeProvider.Capabilities(), modelInfo, snapshot, cfg.FeatureSettings()) {
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
	return &turn.ImageGenerationOptions{
		SessionID:  threadID,
		CodexHome:  r.codexHomeForRollout(),
		Provider:   apiProvider,
		Auth:       authHeaders,
		HTTPClient: r.httpClientForConfig(cfg),
		InputItems: r.imageGenerationInputItemsForTurn(threadID, params),
	}, nil
}

func (r *RuntimeRouter) hostedToolsForTurn(params *turn.TurnStartParams) ([]any, error) {
	if turnStartReviewRuntime(params) {
		return nil, nil
	}
	cfg, err := r.effectiveConfigForTurn(params)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, nil
	}
	standaloneWebSearch, err := r.webSearchOptionsForTurn(cfg, params)
	if err != nil {
		return nil, err
	}
	standaloneImageGeneration, err := r.imageGenerationOptionsForTurn(cfg, params)
	if err != nil {
		return nil, err
	}
	modelProviderConfig, err := r.appTurnModelProviderConfig(cfg, params)
	if err != nil {
		return nil, err
	}
	providerInfo, err := model.ProviderForConfigID(configValues(cfg), modelProviderConfig.ProviderID, stringConfigValue(cfg, "openai_base_url"))
	if err != nil {
		return nil, err
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
	} else if resolved, err := r.resolveAuthWithLoginRestrictions(r.codexHomeForRollout()); err == nil && resolved != nil {
		snapshot = &resolved.Auth
	}
	runtimeProvider := model.CreateRuntimeProviderForID(modelProviderConfig.ProviderID, *providerInfo, snapshot)
	modelInfo := r.modelInfoForRuntimeWithConfig(modelProviderConfig.Model, cfg)
	capabilities := runtimeProvider.Capabilities()
	tools := []any{}
	if standaloneWebSearch == nil && modelInfo != nil && !modelInfo.UseResponsesLite && capabilities.WebSearch {
		mode := webSearchModeFromConfig(cfg)
		if hosted := turn.HostedWebSearchTool(mode, webSearchSettingsFromConfig(cfg, mode), modelInfo.WebSearchToolType); hosted != nil {
			tools = append(tools, hosted)
		}
	}
	if standaloneImageGeneration == nil && appImageGenerationHostedEnabled(*providerInfo, capabilities, modelInfo, snapshot, cfg.FeatureSettings()) {
		tools = append(tools, turn.HostedImageGenerationTool("png"))
	}
	return tools, nil
}

func (r *RuntimeRouter) imageGenerationInputItemsForTurn(threadID string, params *turn.TurnStartParams) []any {
	historyItems, _ := r.historyInputItemsForTurn(threadID)
	items := append([]any(nil), historyItems...)
	var inputs []turn.TurnUserInput
	if params != nil {
		inputs = params.Input
	}
	if current := userMessageInputItemFromTurnUserInputs(promptFromTurnStart(params), inputs); current != nil {
		items = append(items, current)
	}
	return items
}

func appImageGenerationStandaloneEnabled(provider model.ProviderInfo, capabilities model.ProviderCapabilities, info *model.ModelInfo, snapshot *auth.AuthDotJSON, featureSettings map[string]bool) bool {
	if info == nil {
		return false
	}
	if !capabilities.NamespaceTools || !capabilities.ImageGeneration {
		return false
	}
	if !appModelInfoSupportsImageInput(info) {
		return false
	}
	if !features.Enabled(featureSettings, "image_generation") {
		return false
	}
	if account := auth.AccountFromAuth(snapshot); account != nil && account.PlanType == auth.PlanFree {
		return false
	}
	return appImageGenerationStandaloneAuthEnabled(provider, snapshot)
}

func appImageGenerationHostedEnabled(provider model.ProviderInfo, capabilities model.ProviderCapabilities, info *model.ModelInfo, snapshot *auth.AuthDotJSON, featureSettings map[string]bool) bool {
	if info == nil || info.UseResponsesLite {
		return false
	}
	if !capabilities.ImageGeneration {
		return false
	}
	if !appModelInfoSupportsImageInput(info) {
		return false
	}
	if !features.Enabled(featureSettings, "image_generation") {
		return false
	}
	return appImageGenerationAuthEnabled(provider, snapshot)
}

func appImageGenerationStandaloneAuthEnabled(provider model.ProviderInfo, snapshot *auth.AuthDotJSON) bool {
	if provider.UsesOpenAIActorAuthorization() {
		return true
	}
	if snapshot == nil {
		return false
	}
	if appAuthSnapshotUsesCodexBackend(snapshot) {
		return provider.RequiresOpenAIAuth || provider.IsOpenAI()
	}
	return false
}

func appImageGenerationAuthEnabled(provider model.ProviderInfo, snapshot *auth.AuthDotJSON) bool {
	if provider.UsesOpenAIActorAuthorization() {
		return true
	}
	if snapshot == nil {
		return false
	}
	if appAuthSnapshotUsesCodexBackend(snapshot) {
		return provider.RequiresOpenAIAuth || provider.IsOpenAI()
	}
	return provider.IsOpenAI() && snapshot.Mode() == "api-key"
}

func appModelInfoSupportsImageInput(info *model.ModelInfo) bool {
	if info == nil {
		return false
	}
	for _, modality := range info.InputModalities {
		if strings.EqualFold(strings.TrimSpace(modality), "image") {
			return true
		}
	}
	return false
}

func appAuthSnapshotUsesCodexBackend(snapshot *auth.AuthDotJSON) bool {
	if snapshot == nil {
		return false
	}
	switch snapshot.Mode() {
	case "chatgpt", "chatgptAuthTokens", "personal-access-token", "agent-identity":
		return true
	default:
		return false
	}
}
