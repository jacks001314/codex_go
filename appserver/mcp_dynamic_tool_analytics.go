package appserver

import (
	"context"
	"strings"

	"codex_go/mcp"
	"codex_go/telemetry"
)

func (r *RuntimeRouter) emitMCPToolCallAnalyticsEvent(ctx context.Context, connectionID string, threadID string, turnID string, item *ThreadItem, runConfig *appTurnRunConfig) {
	if r == nil || r.services.Analytics == nil || item == nil || runConfig == nil || r.threadAnalyticsDisabled(threadID) {
		return
	}
	if threadItemWireType(item) != "mcpToolCall" {
		return
	}
	status := threadItemMCPStatus(item)
	terminalStatus, failureKind, ok := terminalToolOutcome(status)
	if !ok {
		return
	}
	sink, ok := r.services.Analytics.(telemetry.MCPToolCallEventSink)
	if !ok {
		return
	}
	client, ok := r.analyticsAppServerClient(connectionID)
	if !ok {
		return
	}
	if record := r.threadRecordForAnalytics(threadID); record != nil {
		if originator := strings.TrimSpace(record.Metadata.Originator); originator != "" {
			client.ProductClientID = originator
		}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	lineage := r.responsesMetadataLineage(threadID)
	startedAtMS := uint64FromNonNegativeInt64(threadItemInt64FromData(item.Data, "startedAtMs", "started_at_ms"))
	completedAtMS := uint64FromNonNegativeInt64(threadItemInt64FromData(item.Data, "completedAtMs", "completed_at_ms"))
	if completedAtMS == 0 && item.CreatedAt > 0 {
		completedAtMS = uint64(item.CreatedAt)
	}
	itemID := threadItemExternalID(item)
	reviewSummary := r.toolItemReviewSummary(threadID, turnID, itemID)
	base := telemetry.CodexToolItemEventBase{
		ThreadID:                       threadID,
		SessionID:                      strings.TrimSpace(lineage.SessionID),
		TurnID:                         turnID,
		ItemID:                         itemID,
		AppServerClient:                client,
		Runtime:                        telemetry.CurrentRuntimeMetadata(),
		ThreadSource:                   stringPtrIfNotEmpty(lineage.ThreadSource),
		SubagentSource:                 stringPtrIfNotEmpty(lineage.SubagentKind),
		ParentThreadID:                 stringPtrIfNotEmpty(lineage.ParentThreadID),
		ToolName:                       threadItemMCPTool(item),
		ToolEventType:                  r.toolEventTypeForCall(threadID, turnID, itemID),
		StartedAtMS:                    startedAtMS,
		CompletedAtMS:                  completedAtMS,
		DurationMS:                     uint64PtrFromNonNegativeInt64(int64(completedAtMS) - int64(startedAtMS)),
		ExecutionDurationMS:            uint64PtrFromThreadItemData(item, "durationMs", "duration_ms"),
		ReviewCount:                    reviewSummary.ReviewCount,
		GuardianReviewCount:            reviewSummary.GuardianReviewCount,
		UserReviewCount:                reviewSummary.UserReviewCount,
		FinalApprovalOutcome:           reviewSummary.FinalApprovalOutcome,
		TerminalStatus:                 terminalStatus,
		FailureKind:                    failureKind,
		RequestedAdditionalPermissions: reviewSummary.RequestedAdditionalPermissions,
		RequestedNetworkAccess:         reviewSummary.RequestedNetworkAccess,
	}
	r.enrichToolEventBase(&base, threadID, turnID)
	params := telemetry.CodexMCPToolCallEventParams{
		CodexToolItemEventBase: base,
		MCPServerName:          threadItemMCPServer(item),
		MCPToolName:            threadItemMCPTool(item),
		MCPErrorPresent:        threadItemMCPError(item) != nil,
		PluginID:               threadItemStringPtrFromData(item.Data, "pluginId", "plugin_id"),
		ConnectorID:            threadItemStringPtrFromData(item.Data, "connectorId", "connector_id"),
		// Rust #45649/#45716: the classification the call's producer queued before
		// this item completed, if any.
		ElicitationType: r.takeMCPToolCallElicitation(threadID, turnID, itemID),
	}
	r.emitToolEvent(threadID, turnID, &params.CodexToolItemEventBase, func() {
		sink.TrackCodexMCPToolCallEvent(ctx, telemetry.NewCodexMCPToolCallEvent(params))
	})
	// Rust #45716: a host-owned apps call also reports app usage, carrying the
	// same classification.
	if mcp.IsCodexAppsMCPServerName(threadItemMCPServer(item)) {
		modelSlug := ""
		if runConfig != nil {
			modelSlug = strings.TrimSpace(runConfig.Model)
		}
		r.emitCodexAppUsedEvent(ctx, threadID, turnID, connectionID,
			threadItemStringFromData(item.Data, "connectorId", "connector_id"),
			threadItemStringFromData(item.Data, "connectorName", "connector_name"),
			modelSlug, params.ElicitationType)
	}
}

func (r *RuntimeRouter) emitDynamicToolCallAnalyticsEvent(ctx context.Context, connectionID string, threadID string, turnID string, item *ThreadItem, runConfig *appTurnRunConfig) {
	if r == nil || r.services.Analytics == nil || item == nil || runConfig == nil || r.threadAnalyticsDisabled(threadID) {
		return
	}
	if threadItemWireType(item) != "dynamicToolCall" {
		return
	}
	status := threadItemDynamicStatus(item)
	terminalStatus, failureKind, ok := terminalToolOutcome(status)
	if !ok {
		return
	}
	sink, ok := r.services.Analytics.(telemetry.DynamicToolCallEventSink)
	if !ok {
		return
	}
	client, ok := r.analyticsAppServerClient(connectionID)
	if !ok {
		return
	}
	if record := r.threadRecordForAnalytics(threadID); record != nil {
		if originator := strings.TrimSpace(record.Metadata.Originator); originator != "" {
			client.ProductClientID = originator
		}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	lineage := r.responsesMetadataLineage(threadID)
	startedAtMS := uint64FromNonNegativeInt64(threadItemInt64FromData(item.Data, "startedAtMs", "started_at_ms"))
	completedAtMS := uint64FromNonNegativeInt64(threadItemInt64FromData(item.Data, "completedAtMs", "completed_at_ms"))
	if completedAtMS == 0 && item.CreatedAt > 0 {
		completedAtMS = uint64(item.CreatedAt)
	}
	contentCounts := dynamicToolContentAnalyticsCounts(threadItemDynamicContentItems(item))
	reviewSummary := r.toolItemReviewSummary(threadID, turnID, item.ID)
	base := telemetry.CodexToolItemEventBase{
		ThreadID:                       threadID,
		SessionID:                      strings.TrimSpace(lineage.SessionID),
		TurnID:                         turnID,
		ItemID:                         item.ID,
		AppServerClient:                client,
		Runtime:                        telemetry.CurrentRuntimeMetadata(),
		ThreadSource:                   stringPtrIfNotEmpty(lineage.ThreadSource),
		SubagentSource:                 stringPtrIfNotEmpty(lineage.SubagentKind),
		ParentThreadID:                 stringPtrIfNotEmpty(lineage.ParentThreadID),
		ToolName:                       threadItemDynamicTool(item),
		ToolEventType:                  r.toolEventTypeForCall(threadID, turnID, item.ID),
		StartedAtMS:                    startedAtMS,
		CompletedAtMS:                  completedAtMS,
		DurationMS:                     uint64PtrFromNonNegativeInt64(int64(completedAtMS) - int64(startedAtMS)),
		ExecutionDurationMS:            uint64PtrFromThreadItemData(item, "durationMs", "duration_ms"),
		ReviewCount:                    reviewSummary.ReviewCount,
		GuardianReviewCount:            reviewSummary.GuardianReviewCount,
		UserReviewCount:                reviewSummary.UserReviewCount,
		FinalApprovalOutcome:           reviewSummary.FinalApprovalOutcome,
		TerminalStatus:                 terminalStatus,
		FailureKind:                    failureKind,
		RequestedAdditionalPermissions: reviewSummary.RequestedAdditionalPermissions,
		RequestedNetworkAccess:         reviewSummary.RequestedNetworkAccess,
	}
	r.enrichToolEventBase(&base, threadID, turnID)
	params := telemetry.CodexDynamicToolCallEventParams{
		CodexToolItemEventBase: base,
		DynamicToolName:        threadItemDynamicTool(item),
		Success:                threadItemBoolPtrFromData(item.Data, "success"),
		OutputContentItemCount: contentCounts.Total,
		OutputTextItemCount:    contentCounts.Text,
		OutputImageItemCount:   contentCounts.Image,
	}
	r.emitToolEvent(threadID, turnID, &params.CodexToolItemEventBase, func() {
		sink.TrackCodexDynamicToolCallEvent(ctx, telemetry.NewCodexDynamicToolCallEvent(params))
	})
}

func terminalToolOutcome(status string) (string, *string, bool) {
	switch strings.TrimSpace(status) {
	case "completed":
		return telemetry.ToolItemTerminalStatusCompleted, nil, true
	case "failed":
		return telemetry.ToolItemTerminalStatusFailed, stringPtrIfNotEmpty(telemetry.ToolItemFailureKindToolError), true
	default:
		return "", nil, false
	}
}

type dynamicToolContentAnalyticsCount struct {
	Total *uint64
	Text  *uint64
	Image *uint64
}

func dynamicToolContentAnalyticsCounts(value any) dynamicToolContentAnalyticsCount {
	items, ok := value.([]any)
	if !ok {
		return dynamicToolContentAnalyticsCount{}
	}
	var total uint64
	var text uint64
	var image uint64
	for _, item := range items {
		total++
		switch typed := item.(type) {
		case map[string]any:
			switch strings.TrimSpace(threadItemStringFromAnyMap(typed, "type")) {
			case "inputImage", "input_image", "image":
				image++
			default:
				text++
			}
		default:
			text++
		}
	}
	return dynamicToolContentAnalyticsCount{Total: &total, Text: &text, Image: &image}
}
