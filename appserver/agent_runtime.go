package appserver

import (
	"context"
	"net/http"
	"strings"
	"time"

	"codex_go/agent"
	"codex_go/auth"
	"codex_go/compact"
	"codex_go/config"
	"codex_go/eventmap"
	"codex_go/features"
	"codex_go/install"
	"codex_go/model"
	"codex_go/network"
	"codex_go/retainedctx"
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
	// Select the thread's originator catalog for the models metadata the agent
	// uses while assembling a request (Rust #56284). The cached base runner keeps
	// the default manager so another originator can select its own partition.
	streaming.ModelsManager = model.ModelsManagerForOriginator(streaming.ModelsManager, params.Originator)
	streaming.AuthRecoveryHandler = r.authRecoveryEventHandler(params.ThreadID, turnID)
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
		modelReviewer.logAssessment = r.logGuardianAssessmentForThread
		modelReviewer.interrupt = r.interruptTurnForGuardianCircuitBreaker
		modelReviewer.activeTurnID = r.guardianActiveTurnID
		modelReviewer.transcript = r.guardianReviewTranscript
		modelReviewer.transcriptSelection = r.guardianReviewTranscriptSelection
		modelReviewer.transcriptFormat = r.guardianTranscriptFormatForThread
		modelReviewer.feedbackCaptureEnabled = r.guardianFeedbackCaptureEnabled
		modelReviewer.persistFeedback = r.persistGuardianReviewFeedback
		modelReviewer.reviewPlan = r.guardianReviewPlanForTurn
		modelReviewer.contextMode = r.guardianContextModeForTurn
		modelReviewer.reviewEndpoint = r.guardianReviewEndpointForTurn
		modelReviewer.specialty = r.guardianReviewModelSpecialtyForTurn
		modelReviewer.maxToolCallLagFor = r.guardianMaxToolCallLagForTurn
		modelReviewer.reviewTimeout = r.guardianReviewTimeoutForTurn
		modelReviewer.nodeReplAutoReviewRequired = r.guardianReviewNodeReplAutoReviewRequiredForTurn
		modelReviewer.fullAccess = r.guardianFullAccessForTurn
		modelReviewer.approvalsReviewer = r.guardianApprovalsReviewerForTurn
		modelReviewer.requiredReview = r.guardianReviewRequiredForTurn
		modelReviewer.rolloverForTokenBudget = r.guardianReviewTokenBudgetReached
		modelReviewer.permissionProfile = r.guardianReviewPermissionProfileForTurn
		modelReviewer.turnPermissionProfile = r.guardianTurnPermissionProfileForTurn
		modelReviewer.targetEnvironmentAvailable = r.guardianTargetEnvironmentAvailableForTurn
		modelReviewer.latestResponseID = r.guardianLatestResponseIDForTurn
		modelReviewer.nodeReplEvidence = r.guardianReviewNodeReplEvidence
		modelReviewer.syncImageContextWindow = func(threadID, modelID string) (int64, bool) {
			window := r.effectiveModelContextWindowForModel(modelID, &turn.TurnStartParams{ThreadID: threadID})
			if window <= 0 {
				return 0, false
			}
			return window, true
		}
		modelReviewer.installationID = r.guardianInstallationID
		modelReviewer.environment = r.guardianEnvironmentInputItems
		modelReviewer.environmentForOrigin = r.guardianEnvironmentInputItemsForOrigin
		modelReviewer.rootUserAuthorization = r.guardianRootUserAuthorizationForTurn
		modelReviewer.retainedContext = r.guardianRetainedContextForTurn
		modelReviewer.conversationHistory = r.guardianConversationHistoryForTurn
		modelReviewer.fastDecision = r.emitGuardianV2FastDecision
		modelReviewer.metrics = r.services.TurnMetrics
		modelReviewer.blocking = r.requireGuardianReviewBlocking()
		modelReviewer.turnTiming = func(threadID, turnID string) *turn.TimingState {
			active := r.activeRuntimeTurnStateSnapshot(strings.TrimSpace(threadID), strings.TrimSpace(turnID))
			if active == nil {
				return nil
			}
			return active.Timing
		}
		modelReviewer.subagentThread = r.turnThreadIsSubagent
		modelReviewer.tracer = r.requestTracer
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

// guardianLatestResponseIDForTurn resolves the reviewed turn's newest response id
// for the review request's `parent_response_id` client metadata (Rust #45441).
func (r *RuntimeRouter) guardianLatestResponseIDForTurn(threadID, turnID string) string {
	active := r.activeRuntimeTurnStateSnapshot(strings.TrimSpace(threadID), strings.TrimSpace(turnID))
	if active == nil {
		return ""
	}
	return strings.TrimSpace(active.LatestResponseID)
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

// guardianReviewTimeoutForTurn resolves the turn's configured overall Guardian
// review budget (Rust #56371). The second result is false when
// `auto_review.timeout_ms = 0` disables the deadline.
func (r *RuntimeRouter) guardianReviewTimeoutForTurn(threadID, turnID string) (time.Duration, bool) {
	active := r.activeRuntimeTurnStateSnapshot(strings.TrimSpace(threadID), strings.TrimSpace(turnID))
	if active == nil || active.Params == nil {
		return state.ReviewTimeout, true
	}
	cfg, err := r.effectiveConfigForTurn(active.Params)
	if err != nil || cfg == nil {
		return state.ReviewTimeout, true
	}
	return cfg.GuardianReviewTimeout()
}

// guardianReviewEndpointForTurn mirrors Rust #40892's opt-in dedicated
// Guardian route selection: only ChatGPT-backed Codex-backend requests with the
// free_guardian setting use /guardian. API-key, custom-provider, and disabled
// configurations keep the standard /responses route.
func (r *RuntimeRouter) guardianReviewEndpointForTurn(threadID, turnID string) model.ResponsesEndpoint {
	active := r.activeRuntimeTurnStateSnapshot(strings.TrimSpace(threadID), strings.TrimSpace(turnID))
	if active == nil || active.Params == nil {
		return model.ResponsesEndpointResponses
	}
	cfg, err := r.effectiveConfigForTurn(active.Params)
	if err != nil || cfg == nil || !cfg.GuardianV2FreeGuardian() {
		return model.ResponsesEndpointResponses
	}
	providerID := ""
	if active.RunConfig != nil {
		providerID = active.RunConfig.ProviderID
	}
	providerID = firstNonEmpty(providerID, stringConfigValue(cfg, "model_provider"), model.OpenAIProviderID)
	provider, err := model.ProviderForConfigID(configValues(cfg), providerID, stringConfigValue(cfg, "openai_base_url"))
	if err != nil || provider == nil || !provider.SupportsCodexBackendRoutes() {
		return model.ResponsesEndpointResponses
	}
	snapshot := r.requireAccount().AuthSnapshot()
	if snapshot == nil {
		return model.ResponsesEndpointResponses
	}
	switch snapshot.Mode() {
	case "chatgpt", "chatgptAuthTokens", "personal-access-token", "agent-identity":
		return model.ResponsesEndpointGuardian
	default:
		return model.ResponsesEndpointResponses
	}
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

// guardianReviewRequiredForTurn reports whether the reviewed turn's model is
// listed in auto_review.required_on_models. Required models must reach a full
// review instead of reusing a fast approval (#41309).
func (r *RuntimeRouter) guardianReviewRequiredForTurn(threadID, turnID string) bool {
	active := r.activeRuntimeTurnStateSnapshot(strings.TrimSpace(threadID), strings.TrimSpace(turnID))
	if active == nil || active.Params == nil {
		return false
	}
	cfg, err := r.effectiveConfigForTurn(active.Params)
	if err != nil || cfg == nil {
		return false
	}
	modelID := turnParamModel(active.Params)
	if active.RunConfig != nil {
		modelID = firstNonEmpty(active.RunConfig.Model, modelID)
	}
	return autoReviewRequiredForModel(cfg, modelID)
}

// guardianReviewTokenBudgetReached mirrors Rust #41221: the review rollover
// decision resolves the parent turn's captured token-budget preferences against
// the review model's defaults instead of applying the review model defaults
// unconditionally.
func (r *RuntimeRouter) guardianReviewTokenBudgetReached(threadID, turnID string) bool {
	active := r.activeRuntimeTurnStateSnapshot(strings.TrimSpace(threadID), strings.TrimSpace(turnID))
	if active == nil || active.Params == nil || active.RunConfig == nil || !active.RunConfig.TokenBudgetFeatureEnabled {
		return false
	}
	cfg, err := r.effectiveConfigForTurn(active.Params)
	if err != nil || cfg == nil {
		return false
	}
	reviewModel := strings.TrimSpace(r.guardianReviewModelForTurn(threadID, turnID))
	if reviewModel == "" {
		return false
	}
	budget := config.ResolveTokenBudgetConfig(
		active.RunConfig.ConfiguredTokenBudget,
		active.RunConfig.UseModelTokenBudgetDefaults,
		modelTokenBudgetDefaults(r.modelInfoForRuntimeWithConfig(reviewModel, cfg)),
		active.RunConfig.TokenBudgetFeatureEnabled,
	)
	buffer := 0
	if budget != nil && budget.AutoCompactFallbackBufferTokens != nil {
		buffer = *budget.AutoCompactFallbackBufferTokens
	}
	status := r.compactTokenStatusForTurnWithPolicy(threadID, reviewModel, active.Params, func(policy compact.Policy) compact.Policy {
		policy.FallbackBufferTokens = buffer
		return policy
	})
	return status.ShouldCompact
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
	items, _, err := r.turnEnvironmentContextInputItemForTurn(ctx, threadID, turnID, active.Params, cfg)
	if err != nil || len(items) == 0 {
		return nil, err
	}
	return items, nil
}

// guardianEnvironmentInputItemsForOrigin renders the reviewed process's world
// state with the environments it started in, while the review request itself
// keeps the current turn's model settings (Rust #44575).
func (r *RuntimeRouter) guardianEnvironmentInputItemsForOrigin(ctx context.Context, threadID, turnID string, origin *guardianReviewOrigin) ([]any, error) {
	if origin == nil {
		return r.guardianEnvironmentInputItems(ctx, threadID, turnID)
	}
	active := r.activeRuntimeTurnStateSnapshot(strings.TrimSpace(threadID), strings.TrimSpace(turnID))
	if active == nil || active.Params == nil {
		return nil, nil
	}
	cfg, err := r.effectiveConfigForTurn(active.Params)
	if err != nil {
		return nil, err
	}
	params := cloneTurnStartParams(active.Params)
	params.Environments = cloneMapSlice(origin.Environments)
	if cwd := strings.TrimSpace(origin.CWD); cwd != "" {
		params.CWD = cwd
	}
	items, _, err := r.turnEnvironmentContextInputItemForTurn(ctx, threadID, turnID, params, cfg)
	if err != nil || len(items) == 0 {
		return nil, err
	}
	return items, nil
}

// guardianRetainedContextForTurn resolves the thread's retained evidence for the
// review prompt.
//
// A delegated subagent's history carries the parent's adopted instructions, which
// the spawn path marks with Rust's `inherited_user_message` provenance; the
// derivation therefore keeps only the thread's own accepted instructions as
// retained evidence, and a worker's reviewer sees the root-conversation section
// for the parent's authorization instead of it being presented as the worker's.
func (r *RuntimeRouter) guardianRetainedContextForTurn(threadID, turnID string) *retainedctx.RetainedContext {
	if r == nil {
		return nil
	}
	return r.retainedContextForThread(threadID)
}

// guardianRootUserAuthorizationForTurn returns bounded, role-preserving root
// conversation evidence for a MultiAgent V2 worker review so late root-user
// authorization is not lost when the worker transcript lacks it (#39975).
func (r *RuntimeRouter) guardianRootUserAuthorizationForTurn(threadID, turnID string) []state.RootMessage {
	active := r.activeRuntimeTurnStateSnapshot(strings.TrimSpace(threadID), strings.TrimSpace(turnID))
	if active == nil || active.Params == nil {
		return nil
	}
	cfg, err := r.effectiveConfigForTurn(active.Params)
	if err != nil || cfg == nil {
		return nil
	}
	worker, err := r.threadRecord(session.ThreadID(strings.TrimSpace(threadID)), true, false)
	if err != nil || worker == nil || worker.ParentThreadID == "" {
		return nil
	}
	if knownRuntimeMultiAgentVersion(worker.Metadata.MultiAgentVersion) != agent.VersionV2 {
		agentsConfig, agentsErr := cfg.AgentsConfig(r.configBaseDirForAgents())
		if agentsErr != nil {
			agentsConfig = nil
		}
		if r.runtimeMultiAgentVersionForTurn(threadID, cfg, agentsConfig) != agent.VersionV2 {
			return nil
		}
	}
	handoffEnabled := features.Enabled(cfg.FeatureSettings(), "guardian_root_handoff_context")
	toolNamespace := "collaboration"
	if v2Config, v2Err := cfg.MultiAgentV2Config(0); v2Err == nil && v2Config != nil && strings.TrimSpace(v2Config.ToolNamespace) != "" {
		toolNamespace = strings.TrimSpace(v2Config.ToolNamespace)
	}
	return r.guardianRootConversationWithHandoff(threadID, handoffEnabled, worker.Metadata.AgentPath, toolNamespace)
}

// guardianRootConversation returns the last eight root user/final-assistant
// messages, each bounded to 900 tokens. User summaries, synthetic user-action
// wrappers, contextual user fragments, and assistant commentary are excluded.
func (r *RuntimeRouter) guardianRootConversation(workerThreadID string) []state.RootMessage {
	return r.guardianRootConversationWithHandoff(workerThreadID, false, "", "collaboration")
}

func (r *RuntimeRouter) guardianRootConversationWithHandoff(workerThreadID string, handoffEnabled bool, workerPath string, toolNamespace string) []state.RootMessage {
	if r == nil || strings.TrimSpace(workerThreadID) == "" {
		return nil
	}
	record, err := r.threadRecord(session.ThreadID(strings.TrimSpace(workerThreadID)), true, false)
	if err != nil || record == nil || record.ParentThreadID == "" {
		return nil
	}
	seen := map[session.ThreadID]bool{record.ID: true}
	for record.ParentThreadID != "" {
		if seen[record.ParentThreadID] {
			return nil
		}
		seen[record.ParentThreadID] = true
		parent, parentErr := r.threadRecord(record.ParentThreadID, true, true)
		if parentErr != nil || parent == nil {
			return nil
		}
		record = parent
	}
	if record.ID == session.ThreadID(strings.TrimSpace(workerThreadID)) ||
		knownRuntimeMultiAgentVersion(record.Metadata.MultiAgentVersion) == agent.VersionV1 {
		return nil
	}

	rootID := string(record.ID)
	retained := r.retainedContextForThread(rootID)
	missingRootInstructions := retained != nil && retained.HasMissingUserMessages()
	var userCandidates []guardianRootCandidate
	var retainedUserSources []retainedctx.RetainedUserMessage
	if retained != nil {
		// Rust #43472: reconcile surviving local instructions with the retained
		// snapshot by source identity and persisted acceptance order, so queued
		// input that reached history late still sorts where it was accepted.
		reconciled := retainedctx.NewReconciledRetainedContext(retained, guardianRootLiveUserMessages(record.Items))
		userCandidates, retainedUserSources, missingRootInstructions = guardianRootUserCandidatesFromEntries(
			reconciled.OrderedEntries(),
			reconciled.MissingUserMessages,
			r.guardianRootExcerptLookup(record),
		)
	} else {
		userCandidates = guardianRootItemUserCandidates(record.Items)
	}
	deliveredTexts := guardianRootDeliveredTexts(record.Items)
	assistantCandidates := guardianRootLiveAssistantCandidates(record.Items, deliveredTexts)
	var handoffBoundaries []uint64
	if handoffEnabled {
		handoffBoundaries = guardianRootHandoffBoundaries(record.Items, workerPath, toolNamespace)
	}
	prepared := prepareGuardianRootContext(guardianRootPreparationInput{
		userMessages:            userCandidates,
		assistantMessages:       assistantCandidates,
		retainedAssistants:      guardianRootRetainedAssistantCandidates(retained, record.Items),
		retainedUserSources:     retainedUserSources,
		handoffBoundaries:       handoffBoundaries,
		missingRootInstructions: missingRootInstructions,
		missingAssistantContext: retained != nil && retained.HasOmittedAssistantMessages(),
		missingVerifiedAnswers:  retained != nil && !retained.VerifiedAnswersComplete(),
	}, r.guardianRootLegacyUserMessages(record))
	return prepared.messages
}

func guardianRootItemIsUserMessage(item *session.Item) bool {
	if !sessionItemIsUserMessage(item) {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(item.Role), "user") ||
		strings.EqualFold(strings.TrimSpace(item.Type), "user_message")
}

func guardianRootItemIsAgentMessage(item *session.Item) bool {
	if item == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(item.Type)) {
	case "agent_message", "assistant_message":
		return true
	}
	return strings.EqualFold(strings.TrimSpace(item.Role), "assistant")
}

func guardianRootItemIsSummary(item *session.Item) bool {
	if item == nil {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(stringValueFromMap(item.Data, "kind")), "compaction_summary") ||
		guardianTranscriptIsCompactionSummary(item) {
		return true
	}
	return strings.HasPrefix(strings.TrimSpace(guardianRootMessageText(item)), compact.SummaryPrefix+"\n")
}

func guardianRootMessageText(item *session.Item) string {
	if item == nil {
		return ""
	}
	if text := strings.TrimSpace(item.Text); text != "" {
		return text
	}
	if text := strings.TrimSpace(stringValueFromMap(item.Data, "text")); text != "" {
		return text
	}
	var builder strings.Builder
	for _, part := range item.Content {
		if strings.TrimSpace(part.Text) == "" {
			continue
		}
		builder.WriteString(part.Text)
	}
	return strings.TrimSpace(builder.String())
}

func guardianRootContextualUserText(text string) bool {
	return eventmap.IsContextualUserMessageContent([]eventmap.ContentItem{{Kind: eventmap.ContentInputText, Text: text}})
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
	// Prepared is the isolated reviewer configuration and read-only environment
	// set from Rust's GuardianExtension::prepare_reviewer_options (#40742). The
	// inline Go reviewer applies its resolved permissions and session identity at
	// the request boundary; PreparedError fails the review closed.
	Prepared      *guardianReviewerPreparedOptions
	PreparedError string
	// PrepareInput is the parent state the prepared options were derived from.
	// An execution-scoped review re-prepares them with that execution's captured
	// environments while keeping the current review settings (Rust #44575).
	PrepareInput guardianReviewerPrepareInput
	ProviderID   string
	SessionID    string
	Ephemeral    bool
	// NodeReplPolicy is the resolved extra policy for `node_repl`/`cua_repl`
	// reviews (Rust ResolvedAutoReviewMessages::node_repl_policy). The reviewer
	// injects it as its own developer fragment only when the reviewed action
	// qualifies.
	NodeReplPolicy string
	// CyberAccessProgram is the reviewed turn's explicit Cyber program after the
	// reviewer's normalization (Rust guardian-reviewer
	// settings::guardian_cyber_access_program, #52689): either Daybreak
	// selection is reviewed as Blue and Standard is preserved, so the review
	// request samples with the parent turn's selected program.
	CyberAccessProgram string
	// ReasoningSummary is the reviewed action's captured reasoning summary
	// (Rust #44574): the review session samples with the parent step's setting
	// instead of the current catalog default.
	ReasoningSummary string
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
	// Rust #44574 preserves the action's captured metadata when the reviewer
	// falls back to the parent model; only a separately selected reviewer is
	// resolved against the current catalog.
	parentInfo := capturedModelInfoForRunConfig(active.RunConfig, parentModel)
	if parentInfo == nil {
		parentInfo = r.modelInfoForRuntimeWithConfig(parentModel, cfg)
	}
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
	reasoningSummary := strings.TrimSpace(active.RunConfig.ReasoningSummary)
	if active.Params != nil {
		reasoningSummary = firstNonEmpty(reasoningSummary, stringPtrValue(active.Params.Summary))
	}
	plan := guardianReviewPlan{
		Selection:          selection,
		AutoReview:         autoReview,
		Instructions:       guardianReviewInstructions(cfg, autoReview),
		NodeReplPolicy:     guardianNodeReplPolicy(autoReview),
		CyberAccessProgram: model.GuardianCyberAccessProgram(active.RunConfig.CyberAccessProgram),
		ReasoningSummary:   reasoningSummary,
	}
	var parentEnvironments []map[string]any
	if active.Params != nil {
		parentEnvironments = active.Params.Environments
	}
	prepareInput := guardianReviewerPrepareInput{
		ParentConfig:                    cfg,
		ParentEnvironments:              parentEnvironments,
		ParentModel:                     parentModel,
		ReviewerModel:                   selection.Model,
		ReviewerEffort:                  selection.ReasoningEffort,
		BaseInstructions:                guardianReviewBaseInstructions(cfg, autoReview),
		ParentPermissionProfile:         r.guardianParentPermissionProfileForTurn(active, cfg),
		PreserveClientDeveloperMessages: parentInfo != nil && parentInfo.NodeReplAutoReviewRequired,
		Ephemeral:                       active.RunConfig.Ephemeral,
		ProviderID:                      active.RunConfig.ProviderID,
		SessionID:                       active.RunConfig.SessionID,
	}
	plan.PrepareInput = prepareInput
	prepared, prepareErr := guardianPrepareReviewerOptions(prepareInput)
	plan.ProviderID = strings.TrimSpace(active.RunConfig.ProviderID)
	plan.SessionID = strings.TrimSpace(active.RunConfig.SessionID)
	plan.Ephemeral = active.RunConfig.Ephemeral
	if prepareErr != nil {
		plan.PreparedError = prepareErr.Error()
	} else {
		plan.Prepared = prepared
	}
	return plan
}

// guardianContextModeForTurn reads the reviewer history policy from the same
// turn configuration that owns the review evidence. The reviewer captures it
// before preparation so failure metrics retain it (#49294).
func (r *RuntimeRouter) guardianContextModeForTurn(threadID, turnID string) state.GuardianContextMode {
	active := r.activeRuntimeTurnStateSnapshot(strings.TrimSpace(threadID), strings.TrimSpace(turnID))
	if active == nil || active.Params == nil {
		return ""
	}
	cfg, err := r.effectiveConfigForTurn(active.Params)
	if err != nil || cfg == nil {
		return ""
	}
	return guardianContextModeForConfig(cfg)
}

func guardianContextModeForConfig(cfg *config.Config) state.GuardianContextMode {
	if cfg == nil {
		return ""
	}
	if features.Enabled(cfg.FeatureSettings(), "guardian_reuse_parent_compaction") {
		return state.GuardianContextModeThreadOwned
	}
	return state.GuardianContextModeIndependent
}

// guardianNodeReplPolicy mirrors Rust
// ResolvedAutoReviewMessages::node_repl_policy: the catalog's text wins over the
// bundled document.
func guardianNodeReplPolicy(autoReview *model.AutoReviewMessages) string {
	if autoReview != nil && autoReview.NodeReplPolicy != nil {
		return *autoReview.NodeReplPolicy
	}
	return state.GuardianNodeReplPolicy()
}

// guardianReviewBaseInstructions mirrors the reviewer base instructions Rust
// builds in build_guardian_review_session_config: the resolved policy
// substituted into the resolved policy template and terminated by the output
// contract. The catalog wins over the bundled templates and the managed/config
// policy wins over both. This is the value the prepared reviewer config stores
// as `base_instructions` (#40742).
func guardianReviewBaseInstructions(cfg *config.Config, autoReview *model.AutoReviewMessages) string {
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
	extraPolicy := ""
	if cfg != nil {
		if value, ok := cfg.GuardianExtraPolicy(); ok {
			extraPolicy = value
		}
	}
	return state.RenderGuardianPolicyInstructions(policy, extraPolicy, policyTemplate, state.GuardianOutputContractPrompt())
}

// guardianReviewInstructions adds the reviewer-only prompt sections (conversation
// history retrieval and the JSON transcript grammar) to the base instructions.
func guardianReviewInstructions(cfg *config.Config, autoReview *model.AutoReviewMessages) string {
	instructions := guardianReviewBaseInstructions(cfg, autoReview)
	if cfg != nil &&
		features.Enabled(cfg.FeatureSettings(), "guardian_conversation_history_tools") &&
		features.Enabled(cfg.FeatureSettings(), "apps") {
		historyPrompt := guardianHistoryInstructions
		if override, ok := cfg.GuardianConversationHistoryPrompt(); ok {
			historyPrompt = override
		}
		if historyPrompt != "" {
			instructions += "\n" + historyPrompt
		}
	}
	format := state.GuardianTranscriptFormatLine
	if cfg != nil {
		format = state.ParseGuardianTranscriptFormat(cfg.GuardianV2TranscriptFormat())
	}
	// Rust #51465 prepends the JSON provenance grammar outside the configured
	// classifier prompt limit; the reviewer path carries no truncation here.
	return state.ApplyGuardianTranscriptInstructions(instructions, format)
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
	modelID := turnParamModel(active.Params)
	if active.RunConfig != nil {
		modelID = firstNonEmpty(active.RunConfig.Model, modelID)
	}
	info := capturedModelInfoForRunConfig(active.RunConfig, modelID)
	if info == nil {
		info = r.modelInfoForRuntimeWithConfig(modelID, cfg)
	}
	return info != nil && info.NodeReplAutoReviewRequired
}

// capturedModelInfoForRunConfig returns the turn's captured model metadata when
// it describes the requested model, mirroring Rust's GuardianReviewContext
// retaining `Arc<ModelInfo>` from the reviewed step (#44574).
func capturedModelInfoForRunConfig(runConfig *appTurnRunConfig, modelID string) *model.ModelInfo {
	if runConfig == nil || runConfig.ModelInfo == nil {
		return nil
	}
	captured := strings.TrimSpace(runConfig.ModelInfo.Slug)
	if captured != "" && !strings.EqualFold(captured, strings.TrimSpace(modelID)) {
		return nil
	}
	return runConfig.ModelInfo
}

// guardianParentPermissionProfileForTurn resolves the reviewed turn's own
// permission profile, the value Rust's prepare_reviewer_options intersects with
// read-only (#40742). It prefers the selected environment's resolved profile and
// falls back to the turn's sandbox resolution.
func (r *RuntimeRouter) guardianParentPermissionProfileForTurn(active *activeRuntimeTurn, cfg *config.Config) *sandbox.PermissionProfile {
	if r == nil || active == nil || active.Params == nil {
		return nil
	}
	if _, _, profile, _ := r.guardianTurnEnvironmentForTurn(active.Params, ""); profile != nil {
		return profile
	}
	resolution, err := turnSandboxPermissionProfile(cfg, active.Params.CWD, active.Params)
	if err != nil || resolution == nil {
		return nil
	}
	return resolution.Profile
}

func (r *RuntimeRouter) guardianReviewPermissionProfileForTurn(threadID, turnID string) *sandbox.PermissionProfile {
	profile, _, _ := r.guardianTurnPermissionProfileForTurn(threadID, turnID, "")
	if profile == nil {
		return nil
	}
	return guardianReviewerReadOnlyProfile(profile)
}

// guardianTurnPermissionProfileForTurn resolves the reviewed turn's active
// permission profile, working directory and environment selection id. Rust's
// Guardian permission evidence is resolved from the reviewed environment's own
// profile (`core/src/guardian/permissions.rs::for_environment`), falling back to
// the turn's profile, and names the environment it resolved. A non-empty
// requestedEnvironmentID is the tool payload's own environment (Rust's
// `for_tool`); an unavailable one drops the evidence entirely, as Rust's caller
// turns the resolution error into `None`.
func (r *RuntimeRouter) guardianTurnPermissionProfileForTurn(threadID, turnID string, requestedEnvironmentID string) (*sandbox.PermissionProfile, string, *string) {
	active := r.activeRuntimeTurnStateSnapshot(strings.TrimSpace(threadID), strings.TrimSpace(turnID))
	if active == nil || active.Params == nil {
		return nil, "", nil
	}
	cfg, err := r.effectiveConfigForTurn(active.Params)
	if err != nil || cfg == nil {
		return nil, "", nil
	}
	environmentID, environmentCWD, environmentProfile, environmentFound := r.guardianTurnEnvironmentForTurn(active.Params, requestedEnvironmentID)
	if strings.TrimSpace(requestedEnvironmentID) != "" && !environmentFound {
		return nil, "", nil
	}
	if environmentProfile != nil {
		cwd := firstNonEmpty(environmentCWD, active.Params.CWD)
		return environmentProfile, cwd, environmentID
	}
	resolution, err := turnSandboxPermissionProfile(cfg, active.Params.CWD, active.Params)
	if err != nil || resolution == nil || resolution.Profile == nil {
		return nil, "", environmentID
	}
	return resolution.Profile, active.Params.CWD, environmentID
}

// guardianTargetEnvironmentAvailableForTurn mirrors Rust's ReviewAction::validate
// (#47630): an environment-scoped approval may only reach Guardian when the
// action's environment is part of the turn's captured selections.
func (r *RuntimeRouter) guardianTargetEnvironmentAvailableForTurn(threadID, turnID, environmentID string) bool {
	active := r.activeRuntimeTurnStateSnapshot(strings.TrimSpace(threadID), strings.TrimSpace(turnID))
	if active == nil || active.Params == nil {
		return false
	}
	_, _, _, found := r.guardianTurnEnvironmentForTurn(active.Params, strings.TrimSpace(environmentID))
	return found
}

// guardianTurnEnvironmentForTurn resolves the reviewed turn's environment: the
// requested one when the tool payload names it, otherwise the turn's primary
// environment. It returns the selection id, the working directory, the
// environment's own permission profile when the environment config supplies one
// (Rust's `TurnEnvironment::permission_profile_with_workspace_roots`), and
// whether the requested environment was found. A turn that selects no
// environment keeps the parent turn's profile and reports no id.
func (r *RuntimeRouter) guardianTurnEnvironmentForTurn(params *turn.TurnStartParams, requestedEnvironmentID string) (*string, string, *sandbox.PermissionProfile, bool) {
	if r == nil || params == nil {
		return nil, "", nil, false
	}
	environments := r.unifiedExecEnvironmentsForTurn(params)
	if len(environments) == 0 {
		return nil, "", nil, false
	}
	selected := environments[0]
	requested := strings.TrimSpace(requestedEnvironmentID)
	if requested != "" {
		found := false
		for index := range environments {
			if strings.TrimSpace(environments[index].ID) == requested {
				selected = environments[index]
				found = true
				break
			}
		}
		if !found {
			return nil, "", nil, false
		}
	}
	var environmentID *string
	if id := strings.TrimSpace(selected.ID); id != "" {
		environmentID = &id
	}
	return environmentID, strings.TrimSpace(selected.CWD), selected.PermissionProfile, true
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
	// Rust AuthManager::auth() proactively refreshes managed ChatGPT auth that
	// is inside the expiry window or overdue on its eight-day schedule, and the
	// refresh request reports the matching reason (#53991).
	snapshot = r.proactiveRefreshManagedChatGPT(context.Background(), codexHome, cfg, snapshot)
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
	if err := r.applyWorkspaceRoutingToAgent(context.Background(), provider, snapshot, agent); err != nil {
		return nil, err
	}
	agent.StoreOptions = r.authStoreOptions()
	// Rust #45556: the thread manager owns the configured attachment store and
	// hands it to every session it spawns, so a turn's prepared images upload
	// through the host backend instead of the inline default. Delegated
	// subagent sessions are turns on this same router, so each inherits the
	// same store from the thread manager.
	if r.threads != nil {
		agent.ImageStore = r.threads.AttachmentStore()
	}
	agent.AgentIdentity = agentIdentityOptionsForAppTurn(cfg)
	agent.EnableRequestCompression = features.Enabled(cfg.FeatureSettings(), "enable_request_compression")
	agent.OutputTokenReplayEnabled = features.Enabled(cfg.FeatureSettings(), "output_token_replay")
	contentItemKindsEnabled := features.Enabled(cfg.FeatureSettings(), "content_item_kinds")
	agent.ContentItemKindsEnabled = &contentItemKindsEnabled
	// Rust `ApiKeyCyberAccessPrograms::from_config` (#49714): the OpenAI provider
	// plus `api_key_cyber_access_programs`, with no model-discovery conjunct.
	agent.ApiKeyCyberAccessPrograms = model.ApiKeyCyberAccessProgramsFromConfig(
		cfg.FeatureSettings(),
		firstNonEmpty(runConfig.ProviderID, stringConfigValue(cfg, "model_provider"), model.OpenAIProviderID),
	)
	agent.Residency = managedResidencyForConfig(cfg)
	agent.AWS = provider.AWS
	// Rust's SessionTelemetry records codex.api_request from the client's request
	// telemetry; the turn metrics sink is the app-server's session metrics sink.
	agent.Metrics = r.services.TurnMetrics
	// The same session telemetry feeds the client's diagnostic records (Rust's
	// log_event!/trace_event! macros) for the conversation this turn belongs to.
	r.installSessionTelemetry(agent, params.ThreadID)
	return agent, nil
}

// proactiveRefreshManagedChatGPT mirrors the pre-request half of Rust
// AuthManager::auth() (#53991): a managed ChatGPT auth inside the five-minute
// access-token window refreshes with `proactive_expiry`, and one whose
// last_refresh is older than eight days refreshes with `scheduled`. A failed
// refresh is non-fatal, as in Rust, which logs the error and keeps serving the
// cached auth.
func (r *RuntimeRouter) proactiveRefreshManagedChatGPT(ctx context.Context, codexHome string, cfg *config.Config, snapshot *auth.AuthDotJSON) *auth.AuthDotJSON {
	if r == nil || snapshot == nil {
		return snapshot
	}
	generation := r.authChangeGeneration()
	refreshed, _, attempted, err := auth.ProactiveRefreshManagedChatGPT(ctx, &auth.RefreshChatGPTTokenOptions{
		CodexHome:    codexHome,
		HTTPClient:   r.policyHTTPClientForConfig(cfg),
		AuthSnapshot: snapshot,
		StoreOptions: r.authStoreOptions(),
	}, time.Now())
	if !attempted || err != nil || refreshed == nil {
		return snapshot
	}
	if !r.publishReloadedAuth(generation, refreshed) {
		// A login or logout landed while the refresh was in flight; build the
		// agent from the newer cached credential instead (Rust #55646).
		return r.requireAccount().AuthSnapshot()
	}
	return refreshed
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
	var base model.HTTPDoer
	if r != nil && r.services.HTTPClient != nil {
		base = r.services.HTTPClient
	} else {
		base = network.NewHTTPClient(cfg != nil && cfg.RespectSystemProxyEnabled(), 0)
	}
	if r == nil {
		return base
	}
	// Rust routes every managed app-server client through the shared policy:
	// the destination is checked before route resolution and the permit stays
	// valid for the response body. An unrestricted policy permits every
	// destination, so the shared client is returned unwrapped.
	policy, composed := r.refreshApplicationNetworkPolicy()
	if !policy.IsManaged() || !composed.IsRestricted() {
		return base
	}
	return &network.PolicyHTTPDoer{Policy: policy, Next: base}
}

// policyHTTPClientForConfig returns the app-server's concrete HTTP client with
// the application network policy enforced by the client's own transport. Callers
// that need an `*http.Client` (the realtime WebSocket dial, for example) use
// this instead of asserting a type on httpClientForConfig, whose policy-bound
// result is a `model.HTTPDoer`.
func (r *RuntimeRouter) policyHTTPClientForConfig(cfg *config.Config) *http.Client {
	var base *http.Client
	if r != nil && r.services.HTTPClient != nil {
		if client, ok := r.services.HTTPClient.(*http.Client); ok {
			base = client
		}
	}
	if base == nil {
		base = network.NewHTTPClient(cfg != nil && cfg.RespectSystemProxyEnabled(), 0)
	}
	if r == nil {
		return base
	}
	policy, composed := r.refreshApplicationNetworkPolicy()
	if !policy.IsScoped() || !composed.IsRestricted() {
		return base
	}
	return network.PolicyHTTPClient(policy, base)
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
