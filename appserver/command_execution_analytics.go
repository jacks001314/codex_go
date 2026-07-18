package appserver

import (
	"context"
	"strings"

	"codex_go/telemetry"
)

func (r *RuntimeRouter) emitCommandExecutionAnalyticsEvent(ctx context.Context, connectionID string, threadID string, turnID string, item *ThreadItem, runConfig *appTurnRunConfig) {
	if r == nil || r.services.Analytics == nil || item == nil || runConfig == nil {
		return
	}
	if threadItemWireType(item) != "commandExecution" {
		return
	}
	status := threadItemCommandStatus(item)
	terminalStatus, failureKind, ok := commandExecutionAnalyticsOutcome(status)
	if !ok {
		return
	}
	sink, ok := r.services.Analytics.(telemetry.CommandExecutionEventSink)
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
	durationMS := uint64PtrFromNonNegativeInt64(int64(completedAtMS) - int64(startedAtMS))
	executionDurationMS := uint64PtrFromThreadItemData(item, "durationMs", "duration_ms")
	exitCode := int32PtrFromThreadItemData(item, "exitCode", "exit_code")
	counts := commandActionAnalyticsCounts(threadItemCommandActions(item))
	reviewSummary := r.toolItemReviewSummary(threadID, turnID, threadItemExternalID(item))
	event := telemetry.NewCodexCommandExecutionEvent(telemetry.CodexCommandExecutionEventParams{
		CodexToolItemEventBase: telemetry.CodexToolItemEventBase{
			ThreadID:                       threadID,
			TurnID:                         turnID,
			ItemID:                         threadItemExternalID(item),
			AppServerClient:                client,
			Runtime:                        telemetry.CurrentRuntimeMetadata(),
			ThreadSource:                   stringPtrIfNotEmpty(lineage.ThreadSource),
			SubagentSource:                 stringPtrIfNotEmpty(lineage.SubagentKind),
			ParentThreadID:                 stringPtrIfNotEmpty(lineage.ParentThreadID),
			ToolName:                       commandExecutionAnalyticsToolName(threadItemCommandSource(item)),
			StartedAtMS:                    startedAtMS,
			CompletedAtMS:                  completedAtMS,
			DurationMS:                     durationMS,
			ExecutionDurationMS:            executionDurationMS,
			ReviewCount:                    reviewSummary.ReviewCount,
			GuardianReviewCount:            reviewSummary.GuardianReviewCount,
			UserReviewCount:                reviewSummary.UserReviewCount,
			FinalApprovalOutcome:           reviewSummary.FinalApprovalOutcome,
			TerminalStatus:                 terminalStatus,
			FailureKind:                    failureKind,
			RequestedAdditionalPermissions: reviewSummary.RequestedAdditionalPermissions,
			RequestedNetworkAccess:         reviewSummary.RequestedNetworkAccess,
		},
		CommandExecutionSource:      commandExecutionSourceAnalyticsValue(threadItemCommandSource(item)),
		ExitCode:                    exitCode,
		CommandTotalActionCount:     counts.Total,
		CommandReadActionCount:      counts.Read,
		CommandListFilesActionCount: counts.ListFiles,
		CommandSearchActionCount:    counts.Search,
		CommandUnknownActionCount:   counts.Unknown,
	})
	sink.TrackCodexCommandExecutionEvent(ctx, event)
}

func commandExecutionAnalyticsOutcome(status CommandExecutionStatus) (string, *string, bool) {
	switch status {
	case CommandExecutionCompleted:
		return telemetry.ToolItemTerminalStatusCompleted, nil, true
	case CommandExecutionFailed:
		return telemetry.ToolItemTerminalStatusFailed, stringPtrIfNotEmpty(telemetry.ToolItemFailureKindToolError), true
	case CommandExecutionDeclined:
		return telemetry.ToolItemTerminalStatusRejected, stringPtrIfNotEmpty(telemetry.ToolItemFailureKindApprovalDenied), true
	default:
		return "", nil, false
	}
}

func commandExecutionAnalyticsToolName(source CommandExecutionSource) string {
	switch source {
	case CommandExecutionSourceUserShell:
		return "user_shell"
	case CommandExecutionSourceUnifiedExecStartup, CommandExecutionSourceUnifiedExecInteraction:
		return "unified_exec"
	default:
		return "shell"
	}
}

func commandExecutionSourceAnalyticsValue(source CommandExecutionSource) string {
	switch source {
	case CommandExecutionSourceUserShell:
		return "user_shell"
	case CommandExecutionSourceUnifiedExecStartup:
		return "unified_exec_startup"
	case CommandExecutionSourceUnifiedExecInteraction:
		return "unified_exec_interaction"
	default:
		return "agent"
	}
}

type commandActionAnalyticsCount struct {
	Total     uint64
	Read      uint64
	ListFiles uint64
	Search    uint64
	Unknown   uint64
}

func commandActionAnalyticsCounts(actions []CommandAction) commandActionAnalyticsCount {
	var counts commandActionAnalyticsCount
	for i := range actions {
		counts.Total++
		switch strings.TrimSpace(actions[i].Type) {
		case "read":
			counts.Read++
		case "listFiles", "list_files":
			counts.ListFiles++
		case "search":
			counts.Search++
		default:
			counts.Unknown++
		}
	}
	return counts
}

func uint64PtrFromThreadItemData(item *ThreadItem, keys ...string) *uint64 {
	if item == nil {
		return nil
	}
	return uint64PtrFromNonNegativeInt64(threadItemInt64FromData(item.Data, keys...))
}

func int32PtrFromThreadItemData(item *ThreadItem, keys ...string) *int32 {
	if item == nil {
		return nil
	}
	value := threadItemInt64PtrFromData(item.Data, keys...)
	if value == nil {
		return nil
	}
	out := int32(*value)
	return &out
}
