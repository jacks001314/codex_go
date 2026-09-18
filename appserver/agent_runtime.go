package appserver

import (
	"context"
	"strings"
	"time"

	"codex_go/auth"
	"codex_go/config"
	"codex_go/features"
	"codex_go/install"
	"codex_go/model"
	"codex_go/network"
	"codex_go/sandbox"
	"codex_go/session"
	"codex_go/state"
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
		modelReviewer.reviewPlan = r.guardianReviewPlanForTurn
		modelReviewer.specialty = r.guardianReviewModelSpecialtyForTurn
		modelReviewer.maxToolCallLagFor = r.guardianMaxToolCallLagForTurn
		modelReviewer.nodeReplAutoReviewRequired = r.guardianReviewNodeReplAutoReviewRequiredForTurn
		modelReviewer.fullAccess = r.guardianFullAccessForTurn
		modelReviewer.approvalsReviewer = r.guardianApprovalsReviewerForTurn
		modelReviewer.permissionProfile = r.guardianReviewPermissionProfileForTurn
		modelReviewer.nodeReplEvidence = r.guardianReviewNodeReplEvidence
		modelReviewer.installationID = r.guardianInstallationID
		modelReviewer.environment = r.guardianEnvironmentInputItems
		modelReviewer.rootUserAuthorization = r.guardianRootUserAuthorizationForTurn
		modelReviewer.fastDecision = r.emitGuardianV2FastDecision
		modelReviewer.metrics = r.services.TurnMetrics
		modelReviewer.subagentThread = r.turnThreadIsSubagent
		modelReviewer.warn = func(threadID, message string) {
			if strings.TrimSpace(message) == "" {
				return
			}
			r.notify(NotificationGuardianWarning, &GuardianWarningNotification{ThreadID: threadID, Message: message})
		}
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

// guardianMaxToolCallLagForTurn resolves the stale-score bound for the review's
// turn, mirroring Rust's GuardianV2Config resolution: the configured
// `[features.guardianv2].max_tool_call_lag` wins, then the model catalog's
// `model_messages.guardian_v2.max_tool_call_lag`, then Rust's default.
func (r *RuntimeRouter) guardianMaxToolCallLagForTurn(threadID, turnID string) int {
	active := r.activeRuntimeTurnStateSnapshot(strings.TrimSpace(threadID), strings.TrimSpace(turnID))
	if active == nil || active.Params == nil {
		return defaultGuardianMaxToolCallLag
	}
	var cfg *config.Config
	if effective, err := r.effectiveConfigForTurn(active.Params); err == nil {
		cfg = effective
	}
	info := r.modelInfoForRuntimeWithConfig(strings.TrimSpace(active.Params.Model), cfg)
	return guardianMaxToolCallLag(cfg, info)
}

// guardianMaxToolCallLag resolves the bound from a config plus model info
// (Rust `configured.max_tool_call_lag.or(model_defaults.max_tool_call_lag)`
// with `DEFAULT_MAX_TOOL_CALL_LAG` as the fallback).
func guardianMaxToolCallLag(cfg *config.Config, info *model.ModelInfo) int {
	if cfg != nil {
		if lag, ok := cfg.GuardianV2MaxToolCallLag(); ok {
			return lag
		}
	}
	if info != nil && info.ModelMessages != nil && info.ModelMessages.GuardianV2 != nil &&
		info.ModelMessages.GuardianV2.MaxToolCallLag != nil && *info.ModelMessages.GuardianV2.MaxToolCallLag > 0 {
		return *info.ModelMessages.GuardianV2.MaxToolCallLag
	}
	return defaultGuardianMaxToolCallLag
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

// guardianReviewPlan is what one review attempt needs from the reviewed turn:
// Rust resolve_review_model's selection, the catalog messages the reviewer
// reads (guardian_model_info), and the base instructions
// build_guardian_review_session_config renders from them.
type guardianReviewPlan struct {
	Selection    model.ApprovalReviewModel
	AutoReview   *model.AutoReviewMessages
	Instructions string
}

// guardianReviewPlanForTurn mirrors Rust resolve_review_model: every review
// attempt resolves the reviewer from the parent turn's model and effort plus the
// current catalog, so a review samples with the selected review model, its
// request-level reasoning effort (#46292), and the reviewer's policy
// instructions.
func (r *RuntimeRouter) guardianReviewPlanForTurn(threadID, turnID string) guardianReviewPlan {
	active := r.activeRuntimeTurnStateSnapshot(strings.TrimSpace(threadID), strings.TrimSpace(turnID))
	if active == nil || active.RunConfig == nil {
		return guardianReviewPlan{}
	}
	cfg, _ := r.effectiveConfigForTurn(active.Params)
	parentModel := strings.TrimSpace(firstNonEmpty(active.RunConfig.Model, turnParamModel(active.Params)))
	parentInfo := r.modelInfoForRuntimeWithConfig(parentModel, cfg)
	if parentInfo == nil {
		parentInfo = &model.ModelInfo{
			Slug:                    parentModel,
			AutoReviewModelOverride: strings.TrimSpace(active.RunConfig.AutoReviewModelOverride),
		}
	}
	selection := model.SelectApprovalReviewModel(
		parentInfo,
		appReasoningEffortForTurn(cfg, active.Params),
		r.guardianReviewPreferredModel(cfg, active.Params),
		r.requireModels().Presets(model.RefreshOffline),
	)
	// Rust resolve_review_model returns the reviewer's catalog entry as a second
	// value: the review model's entry when the catalog lists the preferred
	// review model or the parent model overrode it, and the parent's otherwise.
	catalogInfo := parentInfo
	if selection.Model != "" && (selection.CatalogContainsAutoReview || selection.ModelOverridden) {
		if info := r.modelInfoForRuntimeWithConfig(selection.Model, cfg); info != nil {
			catalogInfo = info
		}
	}
	var autoReview *model.AutoReviewMessages
	if catalogInfo != nil && catalogInfo.ModelMessages != nil && catalogInfo.ModelMessages.AutoReview != nil {
		cloned := *catalogInfo.ModelMessages.AutoReview
		autoReview = &cloned
	}
	return guardianReviewPlan{
		Selection:    selection,
		AutoReview:   autoReview,
		Instructions: guardianReviewInstructions(cfg, autoReview),
	}
}

// guardianReviewInstructions mirrors the reviewer base instructions Rust builds
// in build_guardian_review_session_config: the resolved policy substituted into
// the resolved policy template and terminated by the output contract. The
// catalog wins over the bundled templates and the managed/config policy wins
// over both.
func guardianReviewInstructions(cfg *config.Config, autoReview *model.AutoReviewMessages) string {
	policy, policyTemplate := state.GuardianPolicy(), state.GuardianPolicyTemplate()
	if autoReview != nil {
		if autoReview.Policy != nil {
			policy = *autoReview.Policy
		}
		if autoReview.PolicyTemplate != nil {
			policyTemplate = *autoReview.PolicyTemplate
		}
	}
	if cfg != nil {
		if value, ok := cfg.GuardianPolicyConfig(); ok {
			policy = value
		}
		if value, ok := cfg.GuardianPolicyTemplate(); ok {
			policyTemplate = value
		}
	}
	return state.RenderGuardianPolicyInstructions(policy, policyTemplate, state.GuardianOutputContractPrompt())
}

// guardianReviewModelForTurn keeps the model-only view used by the review
// catalog-hash and specialty helpers.
func (r *RuntimeRouter) guardianReviewModelForTurn(threadID, turnID string) string {
	return strings.TrimSpace(r.guardianReviewPlanForTurn(threadID, turnID).Selection.Model)
}

// guardianReviewPreferredModel mirrors Rust
// RuntimeProvider::approval_review_preferred_model for the reviewed turn: the
// provider's preferred approval-review model (Luna for API-key credentials,
// codex-auto-review otherwise). A config that cannot be resolved falls back to
// the default so the selection stays deterministic.
func (r *RuntimeRouter) guardianReviewPreferredModel(cfg *config.Config, params *turn.TurnStartParams) string {
	if cfg == nil {
		return model.DefaultApprovalReviewPreferredModel
	}
	providerID := firstNonEmpty(providerFromTurnStart(params), stringConfigValue(cfg, "model_provider"), model.OpenAIProviderID)
	providerInfo, err := model.ProviderForConfigID(configValues(cfg), providerID, stringConfigValue(cfg, "openai_base_url"))
	if err != nil || providerInfo == nil {
		return model.DefaultApprovalReviewPreferredModel
	}
	var snapshot *auth.AuthDotJSON
	if resolved, err := r.resolveAuthWithLoginRestrictions(r.codexHomeForRollout()); err == nil && resolved != nil {
		snapshot = &resolved.Auth
	}
	return model.CreateRuntimeProviderWithResidency(providerID, *providerInfo, snapshot, managedResidencyForConfig(cfg)).ApprovalReviewPreferredModel()
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

// guardianInstallationID resolves the Codex installation id attached to
// guardian review turn metadata (Rust #44298). It is best-effort: an
// unavailable id leaves the metadata field empty.
func (r *RuntimeRouter) guardianInstallationID() string {
	if r == nil {
		return ""
	}
	codexHome := strings.TrimSpace(r.codexHomeForRollout())
	if codexHome == "" {
		return ""
	}
	id, _ := install.ResolveInstallationID(codexHome)
	return strings.TrimSpace(id)
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
	runtimeProvider := model.CreateRuntimeProviderWithResidency(
		runConfig.ProviderID,
		*provider,
		snapshot,
		managedResidencyForConfig(cfg),
	)
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
	// Rust's SessionTelemetry records codex.api_request from the client's request
	// telemetry; the turn metrics sink is the app-server's session metrics sink.
	agent.Metrics = r.services.TurnMetrics
	// The same session telemetry feeds the client's diagnostic records (Rust's
	// log_event!/trace_event! macros) for the conversation this turn belongs to.
	r.installSessionTelemetry(agent, params.ThreadID)
	return agent, nil
}

// installSessionTelemetry binds the session telemetry the model runner reports
// its diagnostic records to (the sink half of Rust's SessionTelemetry).
func (r *RuntimeRouter) installSessionTelemetry(agent *model.ResponsesAgentRunner, threadID string) {
	if r == nil || agent == nil {
		return
	}
	agent.Telemetry = r.sessionTelemetryForThread(threadID)
}

func managedResidencyForConfig(cfg *config.Config) string {
	if cfg != nil && cfg.Requirements != nil && cfg.Requirements.EnforceResidency != nil {
		return string(*cfg.Requirements.EnforceResidency)
	}
	return ""
}

// managedResidencyFromRequirements resolves the managed residency requirement
// (Rust `enforce_residency`) from a config-requirements read.
func managedResidencyFromRequirements(response *config.ConfigRequirementsReadResponse) string {
	if response == nil || response.Requirements == nil || response.Requirements.EnforceResidency == nil {
		return ""
	}
	return string(*response.Requirements.EnforceResidency)
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
