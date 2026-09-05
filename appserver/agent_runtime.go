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
	"codex_go/session"
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
		prewarm := true
		if cfg, cfgErr := r.effectiveConfigForTurn(params); cfgErr == nil && cfg != nil {
			if strings.EqualFold(turnApprovalsReviewerForTurn(cfg, params), string(config.ApprovalsReviewerUser)) || turnIsFullAccess(cfg, firstNonEmpty(params.CWD, r.services.DefaultCWD), params) {
				prewarm = false
			}
		}
		r.ensureGuardianReviewerWithPrewarm(agent, prewarm)
		return agent
	}
	r.services.Agent = &model.UnavailableAgentRunner{Err: err}
	return r.services.Agent
}

func turnIsFullAccess(cfg *config.Config, cwd string, params *turn.TurnStartParams) bool {
	if cfg == nil || turnApprovalPolicyForTurn(cfg, params) != sandbox.ApprovalNever {
		return false
	}
	resolution, err := turnSandboxPermissionProfile(cfg, cwd, params)
	if err != nil || resolution == nil || !permissionProfileIsFullAccess(resolution.Profile) {
		return false
	}
	return turnEnvironmentSelectionsHaveFullAccess(params, resolution.Profile)
}

func permissionProfileIsFullAccess(profile *sandbox.PermissionProfile) bool {
	return profile != nil && !profile.HasDenyReadEntries() &&
		profile.SandboxPolicy != nil && profile.SandboxPolicy.Kind == sandbox.SandboxDangerFullAccess
}

func turnEnvironmentSelectionsHaveFullAccess(params *turn.TurnStartParams, threadProfile *sandbox.PermissionProfile) bool {
	if params == nil || len(params.Environments) == 0 {
		return true
	}
	for _, selection := range params.Environments {
		state, err := environmentConfigStateFromAnyMap(selection)
		if err != nil {
			return false
		}
		switch state.Kind {
		case EnvironmentConfigPending, EnvironmentConfigFailed:
			return false
		case EnvironmentConfigFromThread:
			if !permissionProfileIsFullAccess(threadProfile) {
				return false
			}
		case EnvironmentConfigReady:
			if !permissionProfileIsFullAccess(environmentConfigPermissionProfile(state.Config)) {
				return false
			}
		}
	}
	return true
}

func (r *RuntimeRouter) ensureGuardianReviewer(agent model.AgentRunner) GuardianReviewer {
	return r.ensureGuardianReviewerWithPrewarm(agent, true)
}

func (r *RuntimeRouter) ensureGuardianReviewerWithPrewarm(agent model.AgentRunner, prewarm bool) GuardianReviewer {
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
		modelReviewer.autoReviewMessages = r.guardianReviewAutoReviewMessagesForTurn
		modelReviewer.specialty = r.guardianReviewModelSpecialtyForTurn
		modelReviewer.nodeReplAutoReviewRequired = r.guardianReviewNodeReplAutoReviewRequiredForTurn
		modelReviewer.fullAccess = r.guardianFullAccessForTurn
		modelReviewer.approvalsReviewer = r.guardianApprovalsReviewerForTurn
		modelReviewer.permissionProfile = r.guardianReviewPermissionProfileForTurn
		modelReviewer.nodeReplEvidence = r.guardianReviewNodeReplEvidence
		modelReviewer.environment = r.guardianEnvironmentInputItems
		modelReviewer.rootUserAuthorization = r.guardianRootUserAuthorizationForTurn
		modelReviewer.fastDecision = r.emitGuardianV2FastDecision
		r.services.GuardianReviewer = reviewer
		if prewarm {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				_ = modelReviewer.Prewarm(ctx)
			}()
		}
	}
	return reviewer
}

func (r *RuntimeRouter) guardianFullAccessForTurn(threadID, turnID string) bool {
	active := r.activeRuntimeTurnStateSnapshot(strings.TrimSpace(threadID), strings.TrimSpace(turnID))
	if active == nil || active.Params == nil {
		return false
	}
	cfg, err := r.effectiveConfigForTurn(active.Params)
	if err != nil || cfg == nil {
		return false
	}
	return turnIsFullAccess(cfg, firstNonEmpty(active.Params.CWD, r.services.DefaultCWD), active.Params)
}

func (r *RuntimeRouter) guardianApprovalsReviewerForTurn(threadID, turnID string) string {
	active := r.activeRuntimeTurnStateSnapshot(strings.TrimSpace(threadID), strings.TrimSpace(turnID))
	if active == nil || active.Params == nil {
		return ""
	}
	cfg, err := r.effectiveConfigForTurn(active.Params)
	if err != nil || cfg == nil {
		return ""
	}
	return turnApprovalsReviewerForTurn(cfg, active.Params)
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

// guardianRootUserAuthorizationForTurn returns bounded root-conversation user
// message evidence for a subagent (MultiAgent V2) review so late root-user
// authorization is not lost when the worker transcript lacks it (#39975). The
// real root-thread backfill is a best-effort read from the active worker's root
// thread; when unavailable, the reviewer falls back to the worker transcript.
func (r *RuntimeRouter) guardianRootUserAuthorizationForTurn(threadID, turnID string) []string {
	active := r.activeRuntimeTurnStateSnapshot(strings.TrimSpace(threadID), strings.TrimSpace(turnID))
	if active == nil || active.Params == nil {
		return nil
	}
	cfg, err := r.effectiveConfigForTurn(active.Params)
	if err != nil || cfg == nil {
		return nil
	}
	return r.runtimeRootUserEvidence(threadID)
}

// runtimeRootUserEvidence is a best-effort read of the worker's root user
// message text. It returns nil when no root evidence is accessible.
func (r *RuntimeRouter) runtimeRootUserEvidence(workerThreadID string) []string {
	if r == nil || strings.TrimSpace(workerThreadID) == "" {
		return nil
	}
	record, err := r.threadRecord(session.ThreadID(strings.TrimSpace(workerThreadID)), true, true)
	if err != nil || record == nil {
		return nil
	}
	const maxRootUserEvidenceChars = 3600
	total := 0
	var out []string
	for i := len(record.Items) - 1; i >= 0 && total < maxRootUserEvidenceChars; i-- {
		item := record.Items[i]
		if !sessionItemIsUserMessage(&item) {
			continue
		}
		text := strings.TrimSpace(firstNonEmpty(item.Text, stringValueFromMap(item.Data, "text")))
		if text == "" {
			continue
		}
		chars := len([]rune(text))
		remaining := maxRootUserEvidenceChars - total
		if chars > remaining {
			runes := []rune(text)
			if remaining > 0 {
				text = string(runes[:remaining])
			} else {
				text = ""
			}
			chars = remaining
		}
		if text == "" {
			continue
		}
		out = append(out, text)
		total += chars
	}
	for left, right := 0, len(out)-1; left < right; left, right = left+1, right-1 {
		out[left], out[right] = out[right], out[left]
	}
	return out
}

func sessionItemIsUserMessage(item *session.Item) bool {
	if item == nil {
		return false
	}
	switch strings.TrimSpace(item.Type) {
	case "message", "user_message":
		return strings.EqualFold(strings.TrimSpace(item.Role), "user") || strings.TrimSpace(item.Role) == ""
	default:
		return false
	}
}

func (r *RuntimeRouter) guardianReviewModelForTurn(threadID, turnID string) string {
	active := r.activeRuntimeTurnStateSnapshot(strings.TrimSpace(threadID), strings.TrimSpace(turnID))
	if active == nil || active.RunConfig == nil {
		return ""
	}
	return strings.TrimSpace(active.RunConfig.AutoReviewModelOverride)
}

func (r *RuntimeRouter) guardianReviewModelHashForTurn(threadID, turnID string) string {
	active := r.activeRuntimeTurnStateSnapshot(strings.TrimSpace(threadID), strings.TrimSpace(turnID))
	if active == nil || active.RunConfig == nil {
		return ""
	}
	cfg, err := r.effectiveConfigForTurn(active.Params)
	if err != nil || cfg == nil {
		return ""
	}
	modelID := firstNonEmpty(r.guardianReviewModelForTurn(threadID, turnID), active.RunConfig.Model)
	if info := r.modelInfoForRuntimeWithConfig(modelID, cfg); info != nil {
		return strings.TrimSpace(info.CompHash)
	}
	return ""
}

func (r *RuntimeRouter) guardianReviewAutoReviewMessagesForTurn(threadID, turnID string) *model.AutoReviewMessages {
	active := r.activeRuntimeTurnStateSnapshot(strings.TrimSpace(threadID), strings.TrimSpace(turnID))
	if active == nil || active.Params == nil {
		return nil
	}
	cfg, err := r.effectiveConfigForTurn(active.Params)
	if err != nil || cfg == nil {
		return nil
	}
	info := r.modelInfoForRuntimeWithConfig(strings.TrimSpace(active.Params.Model), cfg)
	if info == nil || info.ModelMessages == nil || info.ModelMessages.AutoReview == nil {
		return nil
	}
	autoReview := *info.ModelMessages.AutoReview
	return &autoReview
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
	contentItemKindsEnabled := features.Enabled(cfg.FeatureSettings(), "content_item_kinds")
	agent.ContentItemKindsEnabled = &contentItemKindsEnabled
	agent.Residency = managedResidencyForConfig(cfg)
	agent.FreeGuardianEnabled = cfg.FreeGuardianEnabled()
	agent.AWS = provider.AWS
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
