package appserver

import (
	"context"
	"strings"

	"codex_go/internal/session"
	"codex_go/internal/telemetry"
)

const (
	threadInitializationModeNew     = "new"
	threadInitializationModeResumed = "resumed"
	threadInitializationModeForked  = "forked"
)

func (r *RuntimeRouter) emitThreadStartAnalytics(ctx context.Context, connectionID string, response *ThreadStartResponse, request *Request) {
	if response == nil || response.Thread == nil {
		return
	}
	r.emitCodexThreadInitializedAnalyticsEvent(ctx, connectionID, response.Thread, response.Model, threadInitializationModeNew, r.threadStartOriginatorForAnalytics(response.Thread.ID, request))
}

func (r *RuntimeRouter) emitThreadResumeAnalytics(ctx context.Context, connectionID string, response *ThreadResumeResponse, request *Request) {
	if response == nil || response.Thread == nil {
		return
	}
	modelID := firstNonEmpty(strings.TrimSpace(response.Model), r.threadRecordModelForAnalytics(response.Thread.ID))
	r.emitCodexThreadInitializedAnalyticsEvent(ctx, connectionID, response.Thread, modelID, threadInitializationModeResumed, r.threadRecordOriginatorForAnalytics(response.Thread.ID))
}

func (r *RuntimeRouter) emitThreadForkAnalytics(ctx context.Context, connectionID string, response *ThreadForkResponse, request *Request) {
	if response == nil || response.Thread == nil {
		return
	}
	modelID := firstNonEmpty(strings.TrimSpace(response.Model), r.threadRecordModelForAnalytics(response.Thread.ID))
	r.emitCodexThreadInitializedAnalyticsEvent(ctx, connectionID, response.Thread, modelID, threadInitializationModeForked, r.threadRecordOriginatorForAnalytics(response.Thread.ID))
}

func (r *RuntimeRouter) emitCodexThreadInitializedAnalyticsEvent(ctx context.Context, connectionID string, thread *Thread, modelID string, initializationMode string, threadOriginator string) {
	if r == nil || r.services.Analytics == nil || thread == nil {
		return
	}
	sink, ok := r.services.Analytics.(telemetry.ThreadInitializedEventSink)
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
	parentThreadID := thread.ParentThreadID
	if initializationMode == threadInitializationModeForked {
		parentThreadID = nil
	}
	event := telemetry.NewCodexThreadInitializedEvent(telemetry.CodexThreadInitializedEventInput{
		ThreadID:           thread.ID,
		SessionID:          firstNonEmpty(strings.TrimSpace(thread.SessionID), strings.TrimSpace(thread.ID)),
		AppServerClient:    client,
		ThreadOriginator:   threadOriginator,
		Runtime:            telemetry.CurrentRuntimeMetadata(),
		Model:              strings.TrimSpace(modelID),
		Ephemeral:          thread.Ephemeral,
		ThreadSource:       threadSourceStringPtr(thread.ThreadSource),
		InitializationMode: initializationMode,
		ParentThreadID:     cloneString(parentThreadID),
		ForkedFromThreadID: cloneString(thread.ForkedFromID),
		CreatedAt:          uint64FromNonNegativeInt64(thread.CreatedAt),
	})
	sink.TrackCodexThreadInitializedEvent(ctx, event)
}

func (r *RuntimeRouter) threadStartOriginatorForAnalytics(threadID string, request *Request) string {
	if request != nil {
		var params ThreadStartParams
		if err := request.DecodeParams(&params); err == nil {
			if originator := strings.TrimSpace(stringPtrValue(params.ServiceName)); originator != "" {
				return originator
			}
		}
	}
	return r.threadRecordOriginatorForAnalytics(threadID)
}

func (r *RuntimeRouter) threadRecordOriginatorForAnalytics(threadID string) string {
	record := r.threadRecordForAnalytics(threadID)
	if record == nil {
		return ""
	}
	return strings.TrimSpace(record.Metadata.Originator)
}

func (r *RuntimeRouter) threadRecordModelForAnalytics(threadID string) string {
	record := r.threadRecordForAnalytics(threadID)
	if record == nil {
		return ""
	}
	return strings.TrimSpace(record.Metadata.Model)
}

func (r *RuntimeRouter) threadRecordForAnalytics(threadID string) *session.Record {
	threadID = strings.TrimSpace(threadID)
	if r == nil || threadID == "" {
		return nil
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, false)
	if err != nil {
		return nil
	}
	return record
}

func threadSourceStringPtr(source *ThreadSource) *string {
	if source == nil {
		return nil
	}
	value := strings.TrimSpace(string(*source))
	if value == "" {
		return nil
	}
	return &value
}

func uint64FromNonNegativeInt64(value int64) uint64 {
	if value < 0 {
		return 0
	}
	return uint64(value)
}
