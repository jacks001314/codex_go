package appserver

import (
	"context"
	"strings"
	"time"

	"codex_go/auth"
	"codex_go/config"
	"codex_go/features"
	"codex_go/model"
	"codex_go/network"
	"codex_go/sandbox"
	"codex_go/turn"
)

func (r *RuntimeRouter) agentForAppTurn(params *turn.TurnStartParams, turnID string) model.AgentRunner {
	agent := r.requireAgentForTurn(params)
	streaming, ok := agent.(*model.ResponsesAgentRunner)
	if !ok || streaming == nil || params == nil {
		return agent
	}
	streaming = streaming.WithStreamHandler(r.responsesStreamHandler(params.ThreadID, turnID, params))
	streaming.ExternalAuthRefresh = r.externalAuthRefresh
	streaming.StoreOptions = r.authStoreOptions()
	if cfg, err := r.effectiveConfigForTurn(params); err == nil {
		streaming.AgentIdentity = agentIdentityOptionsForAppTurn(cfg)
		streaming.EnableRequestCompression = features.Enabled(cfg.FeatureSettings(), "enable_request_compression")
	}
	return streaming
}

func (r *RuntimeRouter) requireAgentForTurn(params *turn.TurnStartParams) model.AgentRunner {
	if r == nil {
		return &model.UnavailableAgentRunner{}
	}
	r.servicesMu.Lock()
	defer r.servicesMu.Unlock()
	if r.services.Agent != nil {
		return r.services.Agent
	}
	agent, err := r.responsesAgentForTurn(params)
	if err == nil && agent != nil {
		r.services.Agent = agent
		r.ensureGuardianReviewer(agent)
		return agent
	}
	r.services.Agent = &model.UnavailableAgentRunner{Err: err}
	return r.services.Agent
}

func (r *RuntimeRouter) ensureGuardianReviewer(agent model.AgentRunner) GuardianReviewer {
	if r == nil {
		return nil
	}
	if r.services.GuardianReviewer != nil {
		return r.services.GuardianReviewer
	}
	reviewer := newModelGuardianReviewer(agent)
	if modelReviewer, ok := reviewer.(*modelGuardianReviewer); ok {
		modelReviewer.notify = r.notifyGuardianReviewEvent
		modelReviewer.interrupt = r.interruptTurnForGuardianCircuitBreaker
		modelReviewer.transcript = r.guardianReviewTranscript
		modelReviewer.model = r.guardianReviewModelForTurn
		modelReviewer.specialty = r.guardianReviewModelSpecialtyForTurn
		modelReviewer.nodeReplAutoReviewRequired = r.guardianReviewNodeReplAutoReviewRequiredForTurn
		modelReviewer.permissionProfile = r.guardianReviewPermissionProfileForTurn
		modelReviewer.nodeReplEvidence = r.guardianReviewNodeReplEvidence
		modelReviewer.environment = r.guardianEnvironmentInputItems
		r.services.GuardianReviewer = reviewer
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			_ = modelReviewer.Prewarm(ctx)
		}()
	}
	return reviewer
}

func (r *RuntimeRouter) guardianEnvironmentInputItems(ctx context.Context, threadID, turnID string) ([]any, error) {
	active := r.activeRuntimeTurnStateSnapshot(strings.TrimSpace(threadID), strings.TrimSpace(turnID))
	if active == nil || active.Params == nil {
		return nil, nil
	}
	cfg, err := r.effectiveConfigForTurn(active.Params)
	if err != nil {
		return nil, err
	}
	item, err := r.turnEnvironmentContextInputItemForTurn(ctx, threadID, active.Params, cfg)
	if err != nil || item == nil {
		return nil, err
	}
	return []any{item}, nil
}

func (r *RuntimeRouter) guardianReviewModelForTurn(threadID, turnID string) string {
	active := r.activeRuntimeTurnStateSnapshot(strings.TrimSpace(threadID), strings.TrimSpace(turnID))
	if active == nil || active.RunConfig == nil {
		return ""
	}
	return strings.TrimSpace(active.RunConfig.AutoReviewModelOverride)
}

func (r *RuntimeRouter) guardianReviewModelSpecialtyForTurn(threadID, turnID string) string {
	active := r.activeRuntimeTurnStateSnapshot(strings.TrimSpace(threadID), strings.TrimSpace(turnID))
	if active == nil || active.Params == nil {
		return ""
	}
	cfg, err := r.effectiveConfigForTurn(active.Params)
	if err != nil || cfg == nil {
		return ""
	}
	info := r.modelInfoForRuntimeWithConfig(strings.TrimSpace(active.Params.Model), cfg)
	if info == nil {
		return ""
	}
	return strings.TrimSpace(info.ModelSpecialty)
}

func (r *RuntimeRouter) guardianReviewNodeReplAutoReviewRequiredForTurn(threadID, turnID string) bool {
	active := r.activeRuntimeTurnStateSnapshot(strings.TrimSpace(threadID), strings.TrimSpace(turnID))
	if active == nil || active.Params == nil {
		return false
	}
	cfg, err := r.effectiveConfigForTurn(active.Params)
	if err != nil || cfg == nil {
		return false
	}
	info := r.modelInfoForRuntimeWithConfig(strings.TrimSpace(active.Params.Model), cfg)
	return info != nil && info.NodeReplAutoReviewRequired
}

func (r *RuntimeRouter) guardianReviewPermissionProfileForTurn(threadID, turnID string) *sandbox.PermissionProfile {
	active := r.activeRuntimeTurnStateSnapshot(strings.TrimSpace(threadID), strings.TrimSpace(turnID))
	if active == nil || active.Params == nil {
		return nil
	}
	cfg, err := r.effectiveConfigForTurn(active.Params)
	if err != nil || cfg == nil {
		return nil
	}
	resolution, err := turnSandboxPermissionProfile(cfg, active.Params.CWD, active.Params)
	if err != nil || resolution == nil || resolution.Profile == nil {
		return nil
	}
	readOnly := resolution.Profile.IntersectWithReadOnly()
	if readOnly == nil {
		profile := sandbox.ReadOnlyPermissionProfile()
		readOnly = &profile
	}
	return readOnly
}

func (r *RuntimeRouter) responsesAgentForTurn(params *turn.TurnStartParams) (*model.ResponsesAgentRunner, error) {
	cfg, err := r.effectiveConfigForTurn(params)
	if err != nil {
		return nil, err
	}
	runConfig, err := r.appTurnModelProviderConfig(cfg, params)
	if err != nil {
		return nil, err
	}
	provider, err := model.ProviderForConfigID(configValues(cfg), runConfig.ProviderID, stringConfigValue(cfg, "openai_base_url"))
	if err != nil {
		return nil, err
	}
	codexHome := r.codexHomeForRollout()
	var snapshot *auth.AuthDotJSON
	if resolved, err := r.resolveAuthWithLoginRestrictions(codexHome); err == nil && resolved != nil {
		snapshot = &resolved.Auth
	} else if err != nil {
		return nil, err
	}
	if snapshot == nil && provider.RequiresOpenAIAuth {
		return nil, nil
	}
	runtimeProvider := model.CreateRuntimeProviderForID(runConfig.ProviderID, *provider, snapshot)
	agent, err := model.NewResponsesAgentRunnerFromRuntimeProviderWithAuth(runConfig.ProviderID, runtimeProvider, r.httpClientForConfig(cfg), codexHome, snapshot)
	if err != nil {
		return nil, err
	}
	agent.StoreOptions = r.authStoreOptions()
	agent.AgentIdentity = agentIdentityOptionsForAppTurn(cfg)
	agent.EnableRequestCompression = features.Enabled(cfg.FeatureSettings(), "enable_request_compression")
	agent.Residency = managedResidencyForConfig(cfg)
	return agent, nil
}

func managedResidencyForConfig(cfg *config.Config) string {
	if cfg != nil && cfg.Requirements != nil && cfg.Requirements.EnforceResidency != nil {
		return string(*cfg.Requirements.EnforceResidency)
	}
	return ""
}

func (r *RuntimeRouter) appTurnModelProviderConfig(cfg *config.Config, params *turn.TurnStartParams) (*appTurnRunConfig, error) {
	modelID := firstNonEmpty(turnParamModel(params), stringConfigValue(cfg, "model"), defaultModelForAppTurn())
	providerID := firstNonEmpty(providerFromTurnStart(params), stringConfigValue(cfg, "model_provider"), model.OpenAIProviderID)
	_, err := model.ProviderForConfigID(configValues(cfg), providerID, stringConfigValue(cfg, "openai_base_url"))
	if err != nil {
		return nil, err
	}
	return &appTurnRunConfig{
		Model:      modelID,
		ProviderID: providerID,
		// Rust 46c3268542: storage is disabled for every Responses request,
		// including requests sent through Azure providers.
		Store: false,
	}, nil
}

func (r *RuntimeRouter) httpClientForConfig(cfg *config.Config) model.HTTPDoer {
	if r != nil && r.services.HTTPClient != nil {
		return r.services.HTTPClient
	}
	return network.NewHTTPClient(cfg != nil && cfg.RespectSystemProxyEnabled(), 0)
}

func configValues(cfg *config.Config) map[string]any {
	if cfg == nil || cfg.Values == nil {
		return map[string]any{}
	}
	return cfg.Values
}

func agentIdentityOptionsForAppTurn(cfg *config.Config) *model.AgentIdentityOptions {
	if cfg == nil || !cfg.FeatureSettings()["use_agent_identity"] {
		return nil
	}
	return &model.AgentIdentityOptions{
		Enabled:                   true,
		ChatGPTBaseURL:            cfg.ChatGPTBaseURL(),
		ForcedChatGPTWorkspaceIDs: cfg.ForcedChatGPTWorkspaceIDs(),
		SessionSource:             "vscode",
	}
}
