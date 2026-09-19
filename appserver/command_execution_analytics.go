package appserver

import (
	"context"
	"strings"

	"codex_go/plugin"
	"codex_go/telemetry"
)

func (r *RuntimeRouter) emitCommandExecutionAnalyticsEvent(ctx context.Context, connectionID string, threadID string, turnID string, item *ThreadItem, runConfig *appTurnRunConfig) {
	if r == nil || r.services.Analytics == nil || item == nil || runConfig == nil || r.threadAnalyticsDisabled(threadID) {
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
	pluginID := threadItemStringPtrFromData(item.Data, "pluginId", "plugin_id")
	scriptPath := safeCommandPluginScriptPath(pluginID, threadItemStringPtrFromData(item.Data, "scriptPath", "script_path"))
	// Rust #45445: the event is attributed to the model that invoked the command.
	// The start-time context wins; only when no start was observed does the
	// completion-time run config stand in (the carry the unified-exec path hands
	// the emitter is already the start-time config).
	modelContext, _ := r.takeToolItemModelContext(threadID, turnID, threadItemExternalID(item))
	if modelContext.ModelSlug == "" && modelContext.ReasoningEffort == "" && runConfig != nil {
		modelContext = modelInvocationContext{
			ModelSlug:       strings.TrimSpace(runConfig.Model),
			ReasoningEffort: strings.TrimSpace(runConfig.ReasoningEffort),
		}
	}
	base := telemetry.CodexToolItemEventBase{
		ThreadID:                       threadID,
		SessionID:                      strings.TrimSpace(lineage.SessionID),
		TurnID:                         turnID,
		ItemID:                         threadItemExternalID(item),
		AppServerClient:                client,
		Runtime:                        telemetry.CurrentRuntimeMetadata(),
		ThreadSource:                   stringPtrIfNotEmpty(lineage.ThreadSource),
		SubagentSource:                 stringPtrIfNotEmpty(lineage.SubagentKind),
		ParentThreadID:                 stringPtrIfNotEmpty(lineage.ParentThreadID),
		ToolName:                       commandExecutionAnalyticsToolName(threadItemCommandSource(item)),
		ToolEventType:                  r.toolEventTypeForCall(threadID, turnID, threadItemExternalID(item)),
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
	}
	r.enrichToolEventBase(&base, threadID, turnID)
	params := telemetry.CodexCommandExecutionEventParams{
		CodexToolItemEventBase:      base,
		ModelSlug:                   optionalModelLabel(modelContext.ModelSlug),
		ReasoningEffort:             optionalModelLabel(modelContext.ReasoningEffort),
		PluginID:                    pluginID,
		ScriptPath:                  scriptPath,
		CommandExecutionSource:      commandExecutionSourceAnalyticsValue(threadItemCommandSource(item)),
		ExitCode:                    exitCode,
		CommandTotalActionCount:     counts.Total,
		CommandReadActionCount:      counts.Read,
		CommandListFilesActionCount: counts.ListFiles,
		CommandSearchActionCount:    counts.Search,
		CommandUnknownActionCount:   counts.Unknown,
	}
	r.emitToolEvent(threadID, turnID, &params.CodexToolItemEventBase, func() {
		sink.TrackCodexCommandExecutionEvent(ctx, telemetry.NewCodexCommandExecutionEvent(params))
	})
}

// optionalModelLabel reports a non-empty label and leaves the field absent (JSON
// null) when the execution carried no context, mirroring Rust's Option fields.
func optionalModelLabel(value string) *string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func safeCommandPluginScriptPath(pluginID *string, scriptPath *string) *string {
	if pluginID == nil || strings.TrimSpace(*pluginID) == "" || scriptPath == nil || !plugin.IsSafePluginRelativePath(*scriptPath) {
		return nil
	}
	path := *scriptPath
	return &path
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
