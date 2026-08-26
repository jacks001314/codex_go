package appserver

import (
	"context"
	"strings"

	"codex_go/telemetry"
)

func (r *RuntimeRouter) emitCollabAgentToolCallAnalyticsEvent(ctx context.Context, connectionID string, threadID string, turnID string, item *ThreadItem, runConfig *appTurnRunConfig) {
	if r == nil || r.services.Analytics == nil || item == nil || runConfig == nil {
		return
	}
	if threadItemWireType(item) != "collabAgentToolCall" {
		return
	}
	terminalStatus, failureKind, ok := collabToolCallAnalyticsOutcome(threadItemCollabAgentToolStatus(item))
	if !ok {
		return
	}
	sink, ok := r.services.Analytics.(telemetry.CollabAgentToolCallEventSink)
	if !ok {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	states := threadItemCollabAgentStates(item)
	completedCount, failedCount := collabAgentAnalyticsStateCounts(states)
	receiverThreadIDs := threadItemStringSliceFromData(item.Data, "receiverThreadIds", "receiver_thread_ids")
	base, ok := r.toolItemAnalyticsBase(connectionID, threadID, turnID, item, collabAgentToolAnalyticsName(threadItemCollabAgentTool(item)), terminalStatus, failureKind, nil)
	if !ok {
		return
	}
	event := telemetry.NewCodexCollabAgentToolCallEvent(telemetry.CodexCollabAgentToolCallEventParams{
		CodexToolItemEventBase:   base,
		SenderThreadID:           threadItemStringFromData(item.Data, "senderThreadId", "sender_thread_id"),
		ReceiverThreadCount:      uint64(len(receiverThreadIDs)),
		ReceiverThreadIDs:        receiverThreadIDs,
		RequestedModel:           threadItemStringPtrFromData(item.Data, "model"),
		RequestedReasoningEffort: threadItemStringPtrFromData(item.Data, "reasoningEffort", "reasoning_effort"),
		AgentStateCount:          uint64PtrFromUint(len(states)),
		CompletedAgentCount:      uint64PtrFromUint(completedCount),
		FailedAgentCount:         uint64PtrFromUint(failedCount),
	})
	sink.TrackCodexCollabAgentToolCallEvent(ctx, event)
}

func (r *RuntimeRouter) emitWebSearchAnalyticsEvent(ctx context.Context, connectionID string, threadID string, turnID string, item *ThreadItem, runConfig *appTurnRunConfig) {
	if r == nil || r.services.Analytics == nil || item == nil || runConfig == nil {
		return
	}
	if threadItemWireType(item) != "webSearch" {
		return
	}
	sink, ok := r.services.Analytics.(telemetry.WebSearchEventSink)
	if !ok {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	base, ok := r.toolItemAnalyticsBase(connectionID, threadID, turnID, item, "web_search", telemetry.ToolItemTerminalStatusCompleted, nil, nil)
	if !ok {
		return
	}
	query := threadItemWebSearchQuery(item)
	event := telemetry.NewCodexWebSearchEvent(telemetry.CodexWebSearchEventParams{
		CodexToolItemEventBase: base,
		WebSearchAction:        webSearchAnalyticsActionKind(item),
		QueryPresent:           strings.TrimSpace(query) != "",
		QueryCount:             webSearchAnalyticsQueryCount(query, item),
	})
	sink.TrackCodexWebSearchEvent(ctx, event)
}

func (r *RuntimeRouter) emitImageGenerationAnalyticsEvent(ctx context.Context, connectionID string, threadID string, turnID string, item *ThreadItem, runConfig *appTurnRunConfig) {
	if r == nil || r.services.Analytics == nil || item == nil || runConfig == nil {
		return
	}
	if threadItemWireType(item) != "imageGeneration" {
		return
	}
	sink, ok := r.services.Analytics.(telemetry.ImageGenerationEventSink)
	if !ok {
		return
	}
	status := firstNonEmpty(threadItemStringFromData(item.Data, "status"), item.Status)
	terminalStatus, failureKind := imageGenerationAnalyticsOutcome(status)
	if ctx == nil {
		ctx = context.Background()
	}
	base, ok := r.toolItemAnalyticsBase(connectionID, threadID, turnID, item, "image_generation", terminalStatus, failureKind, nil)
	if !ok {
		return
	}
	event := telemetry.NewCodexImageGenerationEvent(telemetry.CodexImageGenerationEventParams{
		CodexToolItemEventBase: base,
		RevisedPromptPresent:   threadItemStringPtrFromData(item.Data, "revisedPrompt", "revised_prompt") != nil,
		SavedPathPresent:       threadItemStringPtrFromData(item.Data, "savedPath", "saved_path") != nil,
		TransparentBackground:  threadItemBoolPtrFromData(item.Data, "transparentBackground", "transparent_background"),
	})
	sink.TrackCodexImageGenerationEvent(ctx, event)
}

func (r *RuntimeRouter) toolItemAnalyticsBase(connectionID string, threadID string, turnID string, item *ThreadItem, toolName string, terminalStatus string, failureKind *string, executionDurationMS *uint64) (telemetry.CodexToolItemEventBase, bool) {
	client, ok := r.analyticsAppServerClient(connectionID)
	if !ok {
		return telemetry.CodexToolItemEventBase{}, false
	}
	if record := r.threadRecordForAnalytics(threadID); record != nil {
		if originator := strings.TrimSpace(record.Metadata.Originator); originator != "" {
			client.ProductClientID = originator
		}
	}
	lineage := r.responsesMetadataLineage(threadID)
	rootTurnID := ""
	if active := r.activeRuntimeTurnStateSnapshot(threadID, turnID); active != nil && active.Params != nil {
		rootTurnID = strings.TrimSpace(active.Params.RootTurnID)
	}
	startedAtMS := uint64FromNonNegativeInt64(threadItemInt64FromData(item.Data, "startedAtMs", "started_at_ms"))
	completedAtMS := uint64FromNonNegativeInt64(threadItemInt64FromData(item.Data, "completedAtMs", "completed_at_ms"))
	if completedAtMS == 0 && item.CreatedAt > 0 {
		completedAtMS = uint64(item.CreatedAt)
	}
	reviewSummary := r.toolItemReviewSummary(threadID, turnID, item.ID)
	return telemetry.CodexToolItemEventBase{
		ThreadID:                       threadID,
		TurnID:                         turnID,
		ItemID:                         item.ID,
		AppServerClient:                client,
		Runtime:                        telemetry.CurrentRuntimeMetadata(),
		ThreadSource:                   stringPtrIfNotEmpty(lineage.ThreadSource),
		SubagentSource:                 stringPtrIfNotEmpty(lineage.SubagentKind),
		ParentThreadID:                 stringPtrIfNotEmpty(lineage.ParentThreadID),
		RootTurnID:                     stringPtrIfNotEmpty(rootTurnID),
		ToolName:                       toolName,
		StartedAtMS:                    startedAtMS,
		CompletedAtMS:                  completedAtMS,
		DurationMS:                     uint64PtrFromNonNegativeInt64(int64(completedAtMS) - int64(startedAtMS)),
		ExecutionDurationMS:            executionDurationMS,
		ReviewCount:                    reviewSummary.ReviewCount,
		GuardianReviewCount:            reviewSummary.GuardianReviewCount,
		UserReviewCount:                reviewSummary.UserReviewCount,
		FinalApprovalOutcome:           reviewSummary.FinalApprovalOutcome,
		TerminalStatus:                 terminalStatus,
		FailureKind:                    failureKind,
		RequestedAdditionalPermissions: reviewSummary.RequestedAdditionalPermissions,
		RequestedNetworkAccess:         reviewSummary.RequestedNetworkAccess,
	}, true
}

func collabToolCallAnalyticsOutcome(status CollabAgentToolCallStatus) (string, *string, bool) {
	switch status {
	case CollabAgentToolCallCompleted:
		return telemetry.ToolItemTerminalStatusCompleted, nil, true
	case CollabAgentToolCallFailed:
		return telemetry.ToolItemTerminalStatusFailed, stringPtrIfNotEmpty(telemetry.ToolItemFailureKindToolError), true
	case CollabAgentToolCallInterrupted:
		return telemetry.ToolItemTerminalStatusInterrupted, nil, true
	default:
		return "", nil, false
	}
}

func imageGenerationAnalyticsOutcome(status string) (string, *string) {
	switch strings.TrimSpace(status) {
	case "failed", "error":
		return telemetry.ToolItemTerminalStatusFailed, stringPtrIfNotEmpty(telemetry.ToolItemFailureKindToolError)
	default:
		return telemetry.ToolItemTerminalStatusCompleted, nil
	}
}

func collabAgentToolAnalyticsName(tool CollabAgentTool) string {
	switch tool {
	case CollabAgentToolSpawnAgent:
		return "spawn_agent"
	case CollabAgentToolResumeAgent:
		return "resume_agent"
	case CollabAgentToolWait:
		return "wait_agent"
	case CollabAgentToolCloseAgent:
		return "close_agent"
	case CollabAgentToolSendMessage:
		return "send_message"
	case CollabAgentToolFollowup:
		return "followup_task"
	case CollabAgentToolInterrupt:
		return "interrupt_agent"
	case CollabAgentToolListAgents:
		return "list_agents"
	default:
		return "send_input"
	}
}

func collabAgentAnalyticsStateCounts(states map[string]CollabAgentState) (int, int) {
	completed := 0
	failed := 0
	for _, state := range states {
		switch state.Status {
		case CollabAgentStatusCompleted:
			completed++
		case CollabAgentStatusErrored, CollabAgentStatusShutdown, CollabAgentStatusNotFound:
			failed++
		}
	}
	return completed, failed
}

func webSearchAnalyticsActionKind(item *ThreadItem) *string {
	if item == nil {
		return nil
	}
	raw := threadItemAnyFromData(item.Data, "action", "webSearchAction", "web_search_action")
	if raw == nil {
		return nil
	}
	action, ok := threadItemWebSearchActionFromAny(raw).(map[string]any)
	if !ok {
		return nil
	}
	var value string
	switch threadItemStringFromAnyMap(action, "type") {
	case "search":
		value = "search"
	case "openPage":
		value = "open_page"
	case "findInPage":
		value = "find_in_page"
	default:
		value = "other"
	}
	return &value
}

func webSearchAnalyticsQueryCount(query string, item *ThreadItem) *uint64 {
	if item == nil {
		return nil
	}
	raw := threadItemAnyFromData(item.Data, "action", "webSearchAction", "web_search_action")
	if raw == nil {
		if strings.TrimSpace(query) == "" {
			return nil
		}
		return uint64PtrFromUint(1)
	}
	action, ok := threadItemWebSearchActionFromAny(raw).(map[string]any)
	if !ok {
		return nil
	}
	if threadItemStringFromAnyMap(action, "type") != "search" {
		return nil
	}
	if queries, ok := action["queries"].([]string); ok {
		return uint64PtrFromUint(len(queries))
	}
	if action["query"] != nil {
		return uint64PtrFromUint(1)
	}
	return nil
}

func uint64PtrFromUint(value int) *uint64 {
	out := uint64(value)
	return &out
}
