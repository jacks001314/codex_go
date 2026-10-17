package execserver

import (
	"context"
	"strconv"
	"time"

	"codex_go/tracehook"
)

const (
	execServerRequestEnqueuedEvent  = "codex.exec_server.request_enqueued"
	execServerResponseReceivedEvent = "codex.exec_server.response_received"
	execServerResponseEnqueuedEvent = "codex.exec_server.response_enqueued"
	execServerProcessStartSpan      = "codex.exec_server.process_start"
	execServerPrepareSandboxSpan    = "codex.exec_server.process_prepare_sandbox"
	execServerPrepareSnapshotSpan   = "codex.exec_server.process_prepare_shell_snapshot"
	execServerCaptureSnapshotSpan   = "codex.exec_server.process_capture_shell_snapshot"
	execServerProcessSpawnSpan      = "codex.process.spawn"
	traceSafeTarget                 = "codex_otel.trace_safe"
)

// traceProcessID bounds process identifiers attached to traces. Process
// identifiers are runtime-generated and need no prompt-value sanitization.
func traceProcessID(processID string) string {
	if processID == "" || len(processID) > 64 {
		return ""
	}
	return processID
}

func execServerProcessSpanAttributes(processID string, extra map[string]string) map[string]string {
	attributes := make(map[string]string, len(extra)+1)
	for key, value := range extra {
		attributes[key] = value
	}
	if processID = traceProcessID(processID); processID != "" {
		attributes["process.id"] = processID
	}
	return attributes
}

func startExecServerSpan(
	ctx context.Context,
	name string,
	attributes map[string]string,
) (context.Context, tracehook.Span) {
	if ctx == nil {
		ctx = context.Background()
	}
	parent := tracehook.SpanFromContext(ctx)
	if parent == nil {
		return ctx, nil
	}
	span := parent.StartChildSpan(name, attributes)
	if span == nil {
		return ctx, nil
	}
	return tracehook.WithSpan(ctx, span), span
}

func setExecServerSpanAttribute(ctx context.Context, key string, value int64) {
	span := tracehook.SpanFromContext(ctx)
	if span == nil {
		return
	}
	if value < 0 {
		value = 0
	}
	span.SetAttribute(key, strconv.FormatInt(value, 10))
}

func emitExecServerTraceEvent(ctx context.Context, eventName string, fields map[string]string) {
	span := tracehook.SpanFromContext(ctx)
	if span == nil {
		return
	}
	attributes := make(map[string]string, len(fields)+3)
	attributes["level"] = "INFO"
	attributes["target"] = traceSafeTarget
	attributes["event.name"] = eventName
	for key, value := range fields {
		attributes[key] = value
	}
	span.AddEvent(eventName, attributes)
}

func execServerOffsetNanos(started time.Time) int64 {
	elapsed := time.Since(started).Nanoseconds()
	if elapsed < 0 {
		return 0
	}
	return elapsed
}
