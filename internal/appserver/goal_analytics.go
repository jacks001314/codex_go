package appserver

import (
	"context"
	"strings"

	"codex_go/internal/session"
	"codex_go/internal/telemetry"
)

func (r *RuntimeRouter) emitGoalAnalyticsEvent(ctx context.Context, connectionID string, record *session.Record, goal *Goal, eventKind string, turnID *string) {
	if r == nil || r.services.Analytics == nil || goal == nil || strings.TrimSpace(eventKind) == "" {
		return
	}
	sink, ok := r.services.Analytics.(telemetry.GoalEventSink)
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
	threadID := strings.TrimSpace(goal.ThreadID)
	if threadID == "" {
		return
	}
	if record == nil {
		record = r.threadRecordForAnalytics(threadID)
	}
	lineage := r.responsesMetadataLineage(threadID)
	sessionID := firstNonEmpty(lineage.SessionID, threadID)
	threadOriginator := ""
	threadSource := lineage.ThreadSource
	if record != nil {
		sessionID = firstNonEmpty(strings.TrimSpace(record.SessionID), sessionID)
		threadOriginator = strings.TrimSpace(record.Metadata.Originator)
		threadSource = firstNonEmpty(threadSource, strings.TrimSpace(record.Metadata.ThreadSource))
	}
	cumulativeTokens, cumulativeSeconds := goalAnalyticsCumulativeFields(goal, eventKind)
	event := telemetry.NewCodexGoalEvent(telemetry.CodexGoalEventInput{
		ThreadID:                       threadID,
		SessionID:                      sessionID,
		TurnID:                         cloneString(turnID),
		AppServerClient:                client,
		ThreadOriginator:               threadOriginator,
		Runtime:                        telemetry.CurrentRuntimeMetadata(),
		ThreadSource:                   stringPtrIfNotEmpty(threadSource),
		SubagentSource:                 stringPtrIfNotEmpty(lineage.SubagentKind),
		ParentThreadID:                 stringPtrIfNotEmpty(lineage.ParentThreadID),
		GoalID:                         strings.TrimSpace(goal.GoalID),
		EventKind:                      eventKind,
		GoalStatus:                     goalAnalyticsStatus(goal.Status),
		HasTokenBudget:                 goal.TokenBudget != nil,
		CumulativeTokensAccounted:      cumulativeTokens,
		CumulativeTimeAccountedSeconds: cumulativeSeconds,
	})
	sink.TrackCodexGoalEvent(ctx, event)
}

func goalAnalyticsEventKindForSet(existing *Goal, goal *Goal) string {
	if goal == nil {
		return ""
	}
	if existing == nil {
		return telemetry.GoalEventKindCreated
	}
	if strings.TrimSpace(existing.GoalID) != "" &&
		strings.TrimSpace(goal.GoalID) != "" &&
		strings.TrimSpace(existing.GoalID) != strings.TrimSpace(goal.GoalID) {
		return telemetry.GoalEventKindCreated
	}
	if existing.Status != goal.Status {
		return telemetry.GoalEventKindStatusChanged
	}
	return ""
}

func goalAnalyticsStatus(status GoalStatus) string {
	switch status {
	case GoalUsageLimited:
		return "usage_limited"
	case GoalBudgetLimited:
		return "budget_limited"
	case GoalPaused:
		return "paused"
	case GoalBlocked:
		return "blocked"
	case GoalComplete:
		return "complete"
	default:
		return "active"
	}
}

func goalAnalyticsCumulativeFields(goal *Goal, eventKind string) (*int64, *int64) {
	if goal == nil || eventKind != telemetry.GoalEventKindUsageAccounted {
		return nil, nil
	}
	tokens := goal.TokensUsed
	seconds := goal.TimeUsedSeconds
	return &tokens, &seconds
}
