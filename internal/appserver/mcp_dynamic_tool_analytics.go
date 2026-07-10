package appserver

import (
	"context"
	"strings"

	"codex_go/internal/telemetry"
)

func (r *RuntimeRouter) emitMCPToolCallAnalyticsEvent(ctx context.Context, connectionID string, threadID string, turnID string, item *ThreadItem, runConfig *appTurnRunConfig) {
	if r == nil || r.services.Analytics == nil || item == nil || runConfig == nil {
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
	reviewSummary := r.toolItemReviewSummary(threadID, turnID, item.ID)
	event := telemetry.NewCodexMCPToolCallEvent(telemetry.CodexMCPToolCallEventParams{
		CodexToolItemEventBase: telemetry.CodexToolItemEventBase{
			ThreadID:                       threadID,
			TurnID:                         turnID,
			ItemID:                         item.ID,
			AppServerClient:                client,
			Runtime:                        telemetry.CurrentRuntimeMetadata(),
			ThreadSource:                   stringPtrIfNotEmpty(lineage.ThreadSource),
			SubagentSource:                 stringPtrIfNotEmpty(lineage.SubagentKind),
			ParentThreadID:                 stringPtrIfNotEmpty(lineage.ParentThreadID),
			ToolName:                       threadItemMCPTool(item),
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
		},
		MCPServerName:   threadItemMCPServer(item),
		MCPToolName:     threadItemMCPTool(item),
		MCPErrorPresent: threadItemMCPError(item) != nil,
		PluginID:        threadItemStringPtrFromData(item.Data, "pluginId", "plugin_id"),
	})
	sink.TrackCodexMCPToolCallEvent(ctx, event)
}

func (r *RuntimeRouter) emitDynamicToolCallAnalyticsEvent(ctx context.Context, connectionID string, threadID string, turnID string, item *ThreadItem, runConfig *appTurnRunConfig) {
	if r == nil || r.services.Analytics == nil || item == nil || runConfig == nil {
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
	event := telemetry.NewCodexDynamicToolCallEvent(telemetry.CodexDynamicToolCallEventParams{
		CodexToolItemEventBase: telemetry.CodexToolItemEventBase{
			ThreadID:                       threadID,
			TurnID:                         turnID,
			ItemID:                         item.ID,
			AppServerClient:                client,
			Runtime:                        telemetry.CurrentRuntimeMetadata(),
			ThreadSource:                   stringPtrIfNotEmpty(lineage.ThreadSource),
			SubagentSource:                 stringPtrIfNotEmpty(lineage.SubagentKind),
			ParentThreadID:                 stringPtrIfNotEmpty(lineage.ParentThreadID),
			ToolName:                       threadItemDynamicTool(item),
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
		},
		DynamicToolName:        threadItemDynamicTool(item),
		Success:                threadItemBoolPtrFromData(item.Data, "success"),
		OutputContentItemCount: contentCounts.Total,
		OutputTextItemCount:    contentCounts.Text,
		OutputImageItemCount:   contentCounts.Image,
	})
	sink.TrackCodexDynamicToolCallEvent(ctx, event)
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
