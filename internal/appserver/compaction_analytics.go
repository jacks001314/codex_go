package appserver

import (
	"context"
	"errors"
	"strings"
	"time"

	"codex_go/internal/compact"
	"codex_go/internal/session"
	"codex_go/internal/telemetry"
)

func (r *RuntimeRouter) emitCompactionAnalyticsEvent(ctx context.Context, connectionID string, record *session.Record, request *compact.Request, result *compact.Result, compactErr error, startedAt time.Time, completedAt time.Time, activeContextTokensBefore int64) {
	if r == nil || r.services.Analytics == nil || record == nil || request == nil {
		return
	}
	sink, ok := r.services.Analytics.(telemetry.CompactionEventSink)
	if !ok {
		return
	}
	client, ok := r.analyticsAppServerClient(connectionID)
	if !ok {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	}
	if activeContextTokensBefore <= 0 {
		activeContextTokensBefore = compactionAnalyticsActiveContextTokensFromRecord(record)
	}
	activeContextTokensAfter := compactionAnalyticsActiveContextTokensAfter(result)
	if activeContextTokensAfter <= 0 {
		activeContextTokensAfter = activeContextTokensBefore
	}
	var codexErrorKind *string
	var codexErrorHTTPStatusCode *uint16
	if compactErr != nil {
		if errors.Is(compactErr, ErrCompactHookStopped) && (result == nil || !result.Succeeded()) {
			codexErrorKind = stringPtrIfNotEmpty("turn_aborted")
		} else if !errors.Is(compactErr, ErrCompactHookStopped) {
			fields := turnAnalyticsErrorFieldsFromError(compactErr)
			codexErrorKind = fields.CodexErrorKind
			codexErrorHTTPStatusCode = fields.HTTPStatusCode
		}
	}
	lineage := r.responsesMetadataLineage(request.ThreadID)
	sessionID := firstNonEmpty(strings.TrimSpace(record.SessionID), lineage.SessionID, request.ThreadID)
	event := telemetry.NewCodexCompactionEvent(telemetry.CodexCompactionEventInput{
		ThreadID:                        request.ThreadID,
		SessionID:                       sessionID,
		TurnID:                          strings.TrimSpace(request.TurnID),
		AppServerClient:                 client,
		ThreadOriginator:                strings.TrimSpace(record.Metadata.Originator),
		Runtime:                         telemetry.CurrentRuntimeMetadata(),
		ThreadSource:                    stringPtrIfNotEmpty(firstNonEmpty(lineage.ThreadSource, record.Metadata.ThreadSource)),
		SubagentSource:                  stringPtrIfNotEmpty(lineage.SubagentKind),
		ParentThreadID:                  stringPtrIfNotEmpty(lineage.ParentThreadID),
		Trigger:                         compactionAnalyticsTrigger(request.Trigger),
		Reason:                          compactionAnalyticsReason(request.Reason),
		Implementation:                  compactionAnalyticsImplementation(result),
		Phase:                           compactionAnalyticsPhase(request.Phase),
		Strategy:                        telemetry.CompactionStrategyMemento,
		Status:                          compactionAnalyticsStatus(result, compactErr),
		CodexErrorKind:                  codexErrorKind,
		CodexErrorHTTPStatusCode:        codexErrorHTTPStatusCode,
		ActiveContextTokensBefore:       activeContextTokensBefore,
		ActiveContextTokensAfter:        activeContextTokensAfter,
		RetainedImageCount:              compactionAnalyticsRetainedImageCount(result),
		CompactionSummaryTokens:         compactionAnalyticsSummaryTokens(result),
		CachedInputTokens:               compactionAnalyticsCachedInputTokens(result),
		CacheWriteInputTokens:           compactionAnalyticsCacheWriteInputTokens(result),
		StartedAt:                       uint64FromNonNegativeInt64(startedAt.UTC().Unix()),
		CompletedAt:                     uint64FromNonNegativeInt64(completedAt.UTC().Unix()),
		DurationMS:                      uint64PtrFromNonNegativeInt64(completedAt.Sub(startedAt).Milliseconds()),
		AutoCompactFallbackTriggered:    boolFromAny(record.Metadata.Extra["auto_compact_fallback_delivered"]),
		AutoCompactFallbackOutcome:      stringPtrIfNotEmpty(stringFromAny(record.Metadata.Extra["auto_compact_fallback_outcome"])),
		AutoCompactFallbackBufferTokens: int64PtrIfPositive(intFromAny(record.Metadata.Extra["auto_compact_fallback_buffer_tokens"])),
	})
	sink.TrackCodexCompactionEvent(ctx, event)
}

func int64PtrIfPositive(value int) *int64 {
	if value <= 0 {
		return nil
	}
	converted := int64(value)
	return &converted
}

func compactionAnalyticsTrigger(trigger compact.Trigger) string {
	switch trigger {
	case compact.TriggerAuto:
		return telemetry.CompactionTriggerAuto
	default:
		return telemetry.CompactionTriggerManual
	}
}

func compactionAnalyticsReason(reason compact.Reason) string {
	switch reason {
	case compact.ReasonContextWindowExceeded, compact.ReasonTokenLimit:
		return telemetry.CompactionReasonContextLimit
	case compact.ReasonModelSwitch:
		return telemetry.CompactionReasonModelDownshift
	default:
		return telemetry.CompactionReasonUserRequested
	}
}

func compactionAnalyticsImplementation(result *compact.Result) string {
	if result != nil && result.Source == compact.SourceRemote {
		return telemetry.CompactionImplementationResponsesCompact
	}
	return telemetry.CompactionImplementationResponses
}

func compactionAnalyticsPhase(phase compact.Phase) string {
	switch phase {
	case compact.PhasePreTurn:
		return telemetry.CompactionPhasePreTurn
	case compact.PhaseMidTurn:
		return telemetry.CompactionPhaseMidTurn
	default:
		return telemetry.CompactionPhaseStandaloneTurn
	}
}

func compactionAnalyticsStatus(result *compact.Result, compactErr error) string {
	if compactErr != nil {
		if errors.Is(compactErr, ErrCompactHookStopped) {
			if result != nil && result.Succeeded() {
				return telemetry.CompactionStatusCompleted
			}
			return telemetry.CompactionStatusInterrupted
		}
		return telemetry.CompactionStatusFailed
	}
	if result == nil {
		return telemetry.CompactionStatusFailed
	}
	switch result.Status {
	case compact.StatusInterrupted:
		return telemetry.CompactionStatusInterrupted
	case compact.StatusFailed:
		return telemetry.CompactionStatusFailed
	default:
		return telemetry.CompactionStatusCompleted
	}
}

func compactionAnalyticsActiveContextTokensFromRecord(record *session.Record) int64 {
	if record == nil {
		return 0
	}
	if record.Metadata.Extra != nil {
		status := compactTokenStatusFromMetadata(record.Metadata.Extra)
		if status.ActiveContextTokens > 0 {
			return int64(status.ActiveContextTokens)
		}
		if usage, ok := record.Metadata.Extra["last_token_usage"].(map[string]any); ok {
			if total := intFromAny(usage["totalTokens"]); total > 0 {
				return int64(total)
			}
		}
	}
	return compactionAnalyticsEstimateContextTokens(compactItemsFromSessionItems(record.Items))
}

func compactionAnalyticsActiveContextTokensAfter(result *compact.Result) int64 {
	if result == nil {
		return 0
	}
	return compactionAnalyticsEstimateContextTokens(result.NewHistory)
}

func compactionAnalyticsEstimateContextTokens(items []compact.Item) int64 {
	var total int64
	for i := range items {
		text := compact.ItemText(&items[i])
		if strings.TrimSpace(text) == "" {
			continue
		}
		tokenish := (len([]rune(text)) + 3) / 4
		if tokenish <= 0 {
			tokenish = 1
		}
		total += int64(tokenish)
	}
	return total
}

func compactionAnalyticsRetainedImageCount(result *compact.Result) *int {
	if result == nil {
		return nil
	}
	count := 0
	for i := range result.NewHistory {
		for j := range result.NewHistory[i].Content {
			if strings.TrimSpace(result.NewHistory[i].Content[j].ImageURL) != "" {
				count++
			}
		}
	}
	if count == 0 {
		return nil
	}
	return &count
}

func compactionAnalyticsSummaryTokens(result *compact.Result) *int64 {
	if result == nil || result.Usage == nil {
		return nil
	}
	value := result.Usage.OutputTokens
	return &value
}

func compactionAnalyticsCachedInputTokens(result *compact.Result) *int64 {
	if result == nil || result.Usage == nil {
		return nil
	}
	value := result.Usage.CachedInputTokens
	return &value
}

func compactionAnalyticsCacheWriteInputTokens(result *compact.Result) *int64 {
	if result == nil || result.Usage == nil {
		return nil
	}
	value := result.Usage.CacheWriteInputTokens
	return &value
}
