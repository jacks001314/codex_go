package telemetry

import (
	"context"
	"testing"
)

// Mirrors Rust #58034: `otel.log_sse_deltas = false` drops successful delta log
// records only; completed and failed records keep reporting.
func TestSSEDeltaLogsSwitchLikeRust(t *testing.T) {
	ctx := context.Background()
	enabled := NewSessionTelemetry(SessionTelemetryMetadata{ConversationID: "thread-1"})
	enabledSink := &recordingLogSink{}
	enabled.Logs = enabledSink
	enabled.RecordSSEDelta(ctx, "response.output_text.delta", 42)
	if len(enabledSink.records) != 1 {
		t.Fatalf("enabled delta records = %d, want 1", len(enabledSink.records))
	}
	fields := logRecordFields(enabledSink.records[0])
	if fields["event.kind"] != "response.output_text.delta" || fields["payload_bytes"] != "42" {
		t.Fatalf("delta fields = %#v", fields)
	}

	disabled := NewSessionTelemetry(SessionTelemetryMetadata{ConversationID: "thread-1"}).WithoutSSEDeltaLogs()
	disabledSink := &recordingLogSink{}
	disabled.Logs = disabledSink
	disabled.RecordSSEDelta(ctx, "response.output_text.delta", 42)
	disabled.RecordSSEEventCompletedFailed(ctx, "stream broke")
	if len(disabledSink.records) != 1 {
		t.Fatalf("disabled records = %d, want only the failure record", len(disabledSink.records))
	}
	if got := logRecordFields(disabledSink.records[0])["error.message"]; got != "stream broke" {
		t.Fatalf("failure record = %#v", logRecordFields(disabledSink.records[0]))
	}
}
