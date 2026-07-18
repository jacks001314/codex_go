package appserver

import (
	"context"
	"errors"
	"strings"
	"time"

	"codex_go/telemetry"
	"codex_go/turn"
)

func (r *RuntimeRouter) emitCodexTurnSteerAnalyticsEvent(ctx context.Context, connectionID string, params *turn.TurnSteerParams, acceptedTurnID *string, result string, rejectionReason *string, createdAt time.Time) {
	if r == nil || r.services.Analytics == nil || params == nil {
		return
	}
	sink, ok := r.services.Analytics.(telemetry.TurnSteerEventSink)
	if !ok {
		return
	}
	client, ok := r.analyticsAppServerClient(connectionID)
	if !ok {
		return
	}
	record := r.threadRecordForAnalytics(params.ThreadID)
	if record == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	lineage := r.responsesMetadataLineage(params.ThreadID)
	threadSnapshot := r.analyticsThreadSnapshot(params.ThreadID)
	event := telemetry.NewCodexTurnSteerEvent(telemetry.CodexTurnSteerEventInput{
		ThreadID:         params.ThreadID,
		SessionID:        firstNonEmpty(lineage.SessionID, threadSnapshot.SessionID, params.ThreadID),
		ExpectedTurnID:   stringPtrIfNotEmpty(params.ExpectedTurnID),
		AcceptedTurnID:   cloneString(acceptedTurnID),
		AppServerClient:  client,
		ThreadOriginator: strings.TrimSpace(record.Metadata.Originator),
		Runtime:          telemetry.CurrentRuntimeMetadata(),
		ThreadSource:     stringPtrIfNotEmpty(lineage.ThreadSource),
		SubagentSource:   stringPtrIfNotEmpty(lineage.SubagentKind),
		ParentThreadID:   stringPtrIfNotEmpty(lineage.ParentThreadID),
		NumInputImages:   countTurnSteerInputImages(params),
		Result:           result,
		RejectionReason:  cloneString(rejectionReason),
		CreatedAt:        uint64FromNonNegativeInt64(createdAt.UTC().Unix()),
	})
	sink.TrackCodexTurnSteerEvent(ctx, event)
}

func turnSteerAnalyticsRejectionReason(err error) *string {
	if err == nil {
		return nil
	}
	var tooLarge *turn.InputTooLargeError
	switch {
	case errors.As(err, &tooLarge):
		return stringPtrIfNotEmpty(telemetry.TurnSteerRejectionInputTooLarge)
	case errors.Is(err, turn.ErrNoActiveTurnToSteer):
		return stringPtrIfNotEmpty(telemetry.TurnSteerRejectionNoActiveTurn)
	case errors.Is(err, turn.ErrExpectedTurnMismatch):
		return stringPtrIfNotEmpty(telemetry.TurnSteerRejectionExpectedMismatch)
	case errors.Is(err, turn.ErrEmptyTurnSteerInput):
		return stringPtrIfNotEmpty(telemetry.TurnSteerRejectionEmptyInput)
	default:
		return nil
	}
}
