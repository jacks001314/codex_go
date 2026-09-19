package appserver

import (
	"context"
	"strings"
	"time"

	"codex_go/codexapi"
	"codex_go/compact"
	"codex_go/install"
	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

const defaultRemoteCompactModel = "gpt-5.4-mini"

// compactionImplementationResponses is Rust's
// CompactionImplementation::Responses wire value: Go's remote compaction sends
// the compaction prompt through the Responses path (the v2 implementation,
// ResponsesCompactionV2, is not modelled).
const compactionImplementationResponses = "responses"

// compactResponsesClientMetadata builds the client metadata a remote compaction
// request carries. Rust builds it from the compaction turn's captured
// TurnMetadataState (`Session::compaction_responses_metadata` ->
// `to_responses_metadata(.., CodexResponsesRequestKind::Compaction)` plus
// `with_window_and_fork_metadata`), so the document keeps the turn's identity,
// lineage, sandbox, analytics and window/fork fields while the request kind
// reports the compaction operation. The captured step settings
// (`model`/`reasoning_effort`) are *not* applied: Rust applies
// `ExecutionMetadata` only on the sampling and MCP paths, so a compaction
// document carries those keys only when the client configured them.
func (r *RuntimeRouter) compactResponsesClientMetadata(record *session.Record, request *compact.Request, compactModel string) map[string]string {
	if r == nil {
		return nil
	}
	threadID := ""
	cwd := ""
	if record != nil {
		threadID = strings.TrimSpace(string(record.ID))
		cwd = strings.TrimSpace(record.Metadata.CWD)
	}
	turnID := ""
	if request != nil {
		threadID = firstNonEmpty(strings.TrimSpace(request.ThreadID), threadID)
		turnID = strings.TrimSpace(request.TurnID)
	}
	if threadID == "" {
		return nil
	}
	active := r.activeRuntimeTurnStateSnapshot(threadID, turnID)
	var params *turn.TurnStartParams
	startedAtMS := int64(0)
	if active != nil {
		params = active.Params
		startedAtMS = active.StartedAtMS
	}
	if params == nil {
		params = &turn.TurnStartParams{ThreadID: threadID, CWD: cwd}
	}
	cfg, err := r.effectiveConfigForTurn(params)
	if err != nil {
		cfg = nil
	}
	installationID := ""
	if r.services.Config != nil {
		if codexHome := strings.TrimSpace(r.services.Config.CodexHome()); codexHome != "" {
			if id, idErr := install.ResolveInstallationID(codexHome); idErr == nil {
				installationID = id
			}
		}
	}
	lineage := r.responsesMetadataLineage(threadID)
	turnModel := ""
	var autoReviewEnabled *bool
	nodeReplAutoReviewRequired := false
	nodeReplDisabled := false
	sandboxMode := ""
	var extraMetadata map[string]string
	var responsesAPIMetadata map[string]string
	if cfg != nil {
		turnModel = stringConfigValue(cfg, "model")
		autoReviewEnabled = autoReviewEnabledForTurn(cfg, params)
		if turnModel != "" {
			if info := r.modelInfoForRuntimeWithConfig(turnModel, cfg); info != nil {
				nodeReplAutoReviewRequired = info.NodeReplAutoReviewRequired
				nodeReplDisabled = info.NodeReplDisabled
			}
		}
		sandboxCWD := firstNonEmpty(turnCWD(params), cwd, r.services.DefaultCWD)
		if profile, profileErr := turnSandboxPermissionProfile(cfg, sandboxCWD, params); profileErr == nil {
			sandboxMode = permissionProfilePolicyTag(profile, sandboxCWD)
		}
		extraMetadata = cfg.ResponsesAPIClientMetadata()
		responsesAPIMetadata = cfg.ResponsesAPIMetadata()
	}
	extraMetadata = turn.MergeClientMetadata(extraMetadata, params.ResponsesAPIMetadata)
	parentTurnID := strings.TrimSpace(params.ParentTurnID)
	rootTurnID := strings.TrimSpace(params.RootTurnID)
	rootTurnID = effectiveRootTurnID(rootTurnID, turnID, parentTurnID, lineage.SubagentHeader)
	var compaction *codexapi.ClientCompactionMetadata
	if request != nil {
		compaction = &codexapi.ClientCompactionMetadata{
			Trigger:        string(request.Trigger),
			Reason:         string(request.Reason),
			Implementation: compactionImplementationResponses,
			Phase:          string(request.Phase),
		}
	}
	return turn.BuildResponsesClientMetadata(&turn.ResponsesClientMetadataOptions{
		InstallationID:             installationID,
		SessionID:                  firstNonEmpty(lineage.SessionID, threadID),
		ThreadID:                   threadID,
		TurnID:                     turnID,
		WindowID:                   threadID + ":1",
		ContextWindowID:            r.contextWindowIDForThread(threadID),
		WindowNumber:               uint64PtrAppserver(r.windowNumberForThread(threadID)),
		RequestKind:                codexapi.ClientRequestCompaction,
		Compaction:                 compaction,
		ForkedFromThreadID:         lineage.ForkedFromThreadID,
		ParentThreadID:             lineage.ParentThreadID,
		ParentTurnID:               parentTurnID,
		RootTurnID:                 rootTurnID,
		SubagentHeader:             lineage.SubagentHeader,
		SubagentKind:               lineage.SubagentKind,
		ThreadSource:               lineage.ThreadSource,
		TurnTrigger:                params.TurnTrigger,
		SandboxMode:                sandboxMode,
		AgentName:                  r.agentNameForThread(threadID),
		AutoReviewEnabled:          autoReviewEnabled,
		NodeReplAutoReviewRequired: &nodeReplAutoReviewRequired,
		NodeReplDisabled:           &nodeReplDisabled,
		AnalyticsEnabled:           r.analyticsEnabledOptionForThread(threadID),
		Extra:                      extraMetadata,
		ResponsesAPIMetadata:       responsesAPIMetadata,
		HistoryIngestRequested:     r.historyIngestRequestedForTurn(cfg, params),
		StartedAtMS:                startedAtMS,
		UseResponsesLite:           r.modelUsesResponsesLite(compactModel),
	})
}

// remoteCompactServiceTierForRecord resolves the service tier for a remote
// compaction request. A subagent thread follows its root thread's tier; for any
// thread the model-support check is applied so an unsupported tier is dropped
// (mirrors Rust step_context.settings.service_tier under #41308).
func (r *RuntimeRouter) remoteCompactServiceTierForRecord(record *session.Record) string {
	if record == nil {
		return ""
	}
	serviceTier := strings.TrimSpace(record.Metadata.ServiceTier)
	if record.ParentThreadID != "" {
		if rootTier := r.subagentRootServiceTier(string(record.ID)); rootTier != "" {
			serviceTier = rootTier
		}
	}
	if serviceTier == "" {
		return ""
	}
	compactModel := firstNonEmpty(record.Metadata.Model, defaultRemoteCompactModel)
	if info := r.modelInfoForRuntime(compactModel); info != nil {
		return model.ServiceTierForRequest(info, serviceTier)
	}
	return ""
}

type agentCompactRunner struct {
	agent       model.AgentRunner
	model       string
	providerID  string
	serviceTier string
	modelHash   string
	// effort is the pinned request effort for the compaction model when
	// reasoning-effort overrides apply (Rust #43796).
	effort string
	// clientMetadata is the compaction turn's Responses client metadata
	// (Rust `Session::compaction_responses_metadata`): the turn-metadata
	// document reports the compaction request kind and the turn's identity.
	clientMetadata map[string]string
	// executedToolCalls is the thread's executed-tool-call recorder; Rust
	// #46044 attaches the recorded Code Mode inventory to compaction prompts.
	executedToolCalls *turn.ExecutedToolCallRecorder
	// executedToolCallMetadataEnabled mirrors the session's
	// `executed_tool_call_metadata` feature: disabled capture strips direct
	// records from the compaction prompt instead of attaching.
	executedToolCallMetadataEnabled bool
}

func (r *agentCompactRunner) Compact(ctx context.Context, request *compact.Request) (*compact.Result, error) {
	if r == nil || r.agent == nil {
		return nil, nil
	}
	if err := request.Validate(); err != nil {
		return nil, err
	}
	clientMetadata := cloneStringMap(r.clientMetadata)
	if clientMetadata == nil {
		clientMetadata = map[string]string{}
	}
	if strings.TrimSpace(clientMetadata[codexapi.ClientCodexTurnMetadataHeader]) == "" {
		// The runner was built without the turn's metadata document, so report
		// the request kind and identity directly. Rust's compaction request
		// kind serializes as "compaction" (CodexResponsesRequestKind::Compaction).
		clientMetadata[codexapi.RequestKindKey] = string(codexapi.ClientRequestCompaction)
		clientMetadata[codexapi.ThreadIDKey] = strings.TrimSpace(request.ThreadID)
		clientMetadata[codexapi.TurnIDKey] = strings.TrimSpace(request.TurnID)
	}
	history := compactItemsForRemoteRequest(request)
	inputItems := inputItemsFromCompactItems(history)
	if r.executedToolCalls != nil {
		inputItems = r.executedToolCalls.AttachToCompactionPrompt(inputItems, r.executedToolCallMetadataEnabled)
	}
	response, err := r.agent.Run(ctx, &model.AgentRequest{
		Prompt:          strings.TrimSpace(request.Prompt),
		Instructions:    remoteCompactInstructions(),
		InputItems:      inputItems,
		Model:           firstNonEmpty(r.model, defaultRemoteCompactModel),
		ProviderID:      strings.TrimSpace(r.providerID),
		TaskKind:        model.AgentTaskRegular,
		ThreadID:        request.ThreadID,
		TurnID:          request.TurnID,
		Store:           false,
		ClientMetadata:  clientMetadata,
		ServiceTier:     r.serviceTier,
		ReasoningEffort: strings.TrimSpace(r.effort),
	})
	if err != nil {
		return nil, err
	}
	summary := strings.TrimSpace(response.Message)
	if summary == "" {
		for i := range response.Items {
			if text := strings.TrimSpace(response.Items[i].Text); text != "" {
				summary = text
				break
			}
		}
	}
	if summary == "" {
		return nil, nil
	}
	return &compact.Result{
		Status:              compact.StatusCompleted,
		Request:             *request,
		Summary:             summary,
		NewHistory:          compact.BuildCompactedHistory(nil, lastUserCompactItems(history, 1), summary),
		CompletedAt:         time.Now().UTC(),
		Source:              compact.SourceRemote,
		ResponseID:          response.ResponseID,
		Model:               response.Model,
		ProviderID:          response.ProviderID,
		CompactionModelHash: r.modelHash,
		Usage:               compactUsageFromAgentUsage(&response.Usage),
	}, nil
}

func compactUsageFromAgentUsage(usage *model.AgentUsage) *compact.Usage {
	if usage == nil {
		return nil
	}
	return &compact.Usage{
		InputTokens:           usage.InputTokens,
		CachedInputTokens:     usage.CachedInputTokens,
		CacheWriteInputTokens: usage.CacheWriteInputTokens,
		OutputTokens:          usage.OutputTokens,
		ReasoningOutputTokens: usage.ReasoningOutputTokens,
	}
}

func remoteCompactInstructions() string {
	return strings.TrimSpace(`You are compacting a Codex conversation for future continuation.
Write a concise but high-fidelity summary that preserves:
- the user's objective and constraints
- important decisions and current plan
- files changed or intended changes
- commands run and notable outputs
- unresolved work and risks

Return only the summary.`)
}

func compactItemsForRemoteRequest(request *compact.Request) []compact.Item {
	if request == nil {
		return nil
	}
	history := append([]compact.Item(nil), request.History...)
	if request.MaxHistoryTokens > 0 {
		history = compact.TrimHistoryToTokenBudget(history, request.MaxHistoryTokens)
	}
	return history
}

func inputItemsFromCompactItems(items []compact.Item) []any {
	sessionItems := sessionItemsFromCompactItems(items, time.Now().UTC())
	return session.InputItemsFromItems(sessionItems, &session.HistoryBuildOptions{IncludeToolOutputs: true})
}

func lastUserCompactItems(items []compact.Item, count int) []compact.Item {
	if count <= 0 {
		return nil
	}
	out := make([]compact.Item, 0, count)
	for i := len(items) - 1; i >= 0 && len(out) < count; i-- {
		item := items[i]
		if item.Type == "message" && item.Role == "user" && item.Kind != "compaction_summary" {
			out = append(out, item)
		}
	}
	for left, right := 0, len(out)-1; left < right; left, right = left+1, right-1 {
		out[left], out[right] = out[right], out[left]
	}
	return out
}
