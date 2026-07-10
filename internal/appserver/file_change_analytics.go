package appserver

import (
	"context"
	"strings"

	"codex_go/internal/telemetry"
)

func (r *RuntimeRouter) emitFileChangeAnalyticsEvent(ctx context.Context, connectionID string, threadID string, turnID string, item *ThreadItem, runConfig *appTurnRunConfig) {
	if r == nil || r.services.Analytics == nil || item == nil || runConfig == nil {
		return
	}
	if threadItemWireType(item) != "fileChange" {
		return
	}
	status := threadItemFileChangeStatus(item)
	terminalStatus, failureKind, ok := fileChangeAnalyticsOutcome(status)
	if !ok {
		return
	}
	sink, ok := r.services.Analytics.(telemetry.FileChangeEventSink)
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
	counts := fileChangeAnalyticsCounts(threadItemFileChanges(item))
	reviewSummary := r.toolItemReviewSummary(threadID, turnID, threadItemExternalID(item))
	event := telemetry.NewCodexFileChangeEvent(telemetry.CodexFileChangeEventParams{
		CodexToolItemEventBase: telemetry.CodexToolItemEventBase{
			ThreadID:                       threadID,
			TurnID:                         turnID,
			ItemID:                         threadItemExternalID(item),
			AppServerClient:                client,
			Runtime:                        telemetry.CurrentRuntimeMetadata(),
			ThreadSource:                   stringPtrIfNotEmpty(lineage.ThreadSource),
			SubagentSource:                 stringPtrIfNotEmpty(lineage.SubagentKind),
			ParentThreadID:                 stringPtrIfNotEmpty(lineage.ParentThreadID),
			ToolName:                       "apply_patch",
			StartedAtMS:                    startedAtMS,
			CompletedAtMS:                  completedAtMS,
			DurationMS:                     durationMS,
			ExecutionDurationMS:            nil,
			ReviewCount:                    reviewSummary.ReviewCount,
			GuardianReviewCount:            reviewSummary.GuardianReviewCount,
			UserReviewCount:                reviewSummary.UserReviewCount,
			FinalApprovalOutcome:           reviewSummary.FinalApprovalOutcome,
			TerminalStatus:                 terminalStatus,
			FailureKind:                    failureKind,
			RequestedAdditionalPermissions: reviewSummary.RequestedAdditionalPermissions,
			RequestedNetworkAccess:         reviewSummary.RequestedNetworkAccess,
		},
		FileChangeCount: counts.Total,
		FileAddCount:    counts.Add,
		FileUpdateCount: counts.Update,
		FileDeleteCount: counts.Delete,
		FileMoveCount:   counts.Move,
	})
	sink.TrackCodexFileChangeEvent(ctx, event)
}

func fileChangeAnalyticsOutcome(status PatchApplyStatus) (string, *string, bool) {
	switch status {
	case PatchApplyCompleted:
		return telemetry.ToolItemTerminalStatusCompleted, nil, true
	case PatchApplyFailed:
		return telemetry.ToolItemTerminalStatusFailed, stringPtrIfNotEmpty(telemetry.ToolItemFailureKindToolError), true
	case PatchApplyDeclined:
		return telemetry.ToolItemTerminalStatusRejected, stringPtrIfNotEmpty(telemetry.ToolItemFailureKindApprovalDenied), true
	default:
		return "", nil, false
	}
}

type fileChangeAnalyticsCount struct {
	Total  uint64
	Add    uint64
	Update uint64
	Delete uint64
	Move   uint64
}

func fileChangeAnalyticsCounts(changes []fileChangeUpdate) fileChangeAnalyticsCount {
	var counts fileChangeAnalyticsCount
	for i := range changes {
		counts.Total++
		switch strings.TrimSpace(changes[i].Kind.Type) {
		case "add":
			counts.Add++
		case "delete":
			counts.Delete++
		case "update":
			if changes[i].Kind.MovePath != nil && strings.TrimSpace(*changes[i].Kind.MovePath) != "" {
				counts.Move++
			} else {
				counts.Update++
			}
		default:
			counts.Update++
		}
	}
	return counts
}
