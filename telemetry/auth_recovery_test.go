package telemetry

import (
	"context"
	"testing"
	"time"

	"codex_go/model"
)

// The recovery record reports the plan position, the outcome, the failed
// response's debug context, and whether the step changed the cached auth, on both
// records.
func TestRecordAuthRecoveryRoutesLogAndTraceLikeRust(t *testing.T) {
	logBodies := make(chan map[string]any, 1)
	logServer := newLogBatchServer(t, logBodies)
	defer logServer.Close()
	traceBodies := make(chan map[string]any, 1)
	traceServer := newTraceBatchServer(t, traceBodies)
	defer traceServer.Close()

	logsClient := NewLogsClient(LogsClientOptions{
		ServiceName:    "codex-app-server",
		Endpoint:       logServer.URL + "/v1/logs",
		ExportInterval: -1,
	})
	tracesClient := NewTracesClient(TracesClientOptions{
		ServiceName:    "codex-app-server",
		Endpoint:       traceServer.URL + "/v1/traces",
		ExportInterval: -1,
	})
	session := NewSessionTelemetry(SessionTelemetryMetadata{ConversationID: "thread-1"})
	session.Logs = logsClient
	stateChanged := true
	span := tracesClient.Tracer().StartSpan("stream_request", nil)
	session.RecordAuthRecovery(WithSpan(context.Background(), span), model.AuthRecoveryRecord{
		Mode:             "managed",
		Step:             "refresh_token",
		Outcome:          "recovery_succeeded",
		RequestID:        "req-401",
		CFRay:            "ray-401",
		AuthError:        "missing_authorization_header",
		AuthErrorCode:    "token_expired",
		AuthStateChanged: &stateChanged,
	})
	span.End()
	if err := logsClient.Flush(context.Background()); err != nil {
		t.Fatalf("logs Flush() error = %v", err)
	}
	if err := tracesClient.Flush(context.Background()); err != nil {
		t.Fatalf("traces Flush() error = %v", err)
	}

	want := map[string]string{
		"event.name":         "codex.auth_recovery",
		"auth.mode":          "managed",
		"auth.step":          "refresh_token",
		"auth.outcome":       "recovery_succeeded",
		"auth.request_id":    "req-401",
		"auth.cf_ray":        "ray-401",
		"auth.error":         "missing_authorization_header",
		"auth.error_code":    "token_expired",
		"auth.state_changed": "true",
		"conversation.id":    "thread-1",
	}
	select {
	case body := <-logBodies:
		_, record := singleLogRecord(t, body)
		attributes := logRecordAttributes(t, record)
		for key, value := range want {
			if got := attributes[key]; got != value {
				t.Fatalf("log attribute %s = %q, want %q", key, got, value)
			}
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the logs endpoint did not receive the recovery record")
	}

	select {
	case body := <-traceBodies:
		spans := body["resourceSpans"].([]any)[0].(map[string]any)["scopeSpans"].([]any)[0].(map[string]any)["spans"].([]any)
		events := spans[0].(map[string]any)["events"].([]any)
		if len(events) != 1 {
			t.Fatalf("span events = %#v", events)
		}
		attributes := spanEventAttributes(t, events[0].(map[string]any))
		for key, value := range want {
			if got := attributes[key]; got != value {
				t.Fatalf("span event attribute %s = %q, want %q", key, got, value)
			}
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the traces endpoint did not receive the recovery event")
	}
}

// A recovery that never ran reports the not-run outcome without a state change,
// and the optional debug context stays absent when the response had none.
func TestRecordAuthRecoveryOmitsAbsentFieldsLikeRust(t *testing.T) {
	logBodies := make(chan map[string]any, 1)
	logServer := newLogBatchServer(t, logBodies)
	defer logServer.Close()
	logsClient := NewLogsClient(LogsClientOptions{
		ServiceName:    "codex-app-server",
		Endpoint:       logServer.URL + "/v1/logs",
		ExportInterval: -1,
	})
	session := NewSessionTelemetry(SessionTelemetryMetadata{ConversationID: "thread-1"})
	session.Logs = logsClient
	session.RecordAuthRecovery(context.Background(), model.AuthRecoveryRecord{
		Mode:    "none",
		Step:    "none",
		Outcome: "recovery_not_run",
	})
	if err := logsClient.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	body := <-logBodies
	_, record := singleLogRecord(t, body)
	attributes := logRecordAttributes(t, record)
	for _, absent := range []string{"auth.request_id", "auth.cf_ray", "auth.error", "auth.error_code", "auth.recovery_reason", "auth.state_changed"} {
		if _, ok := attributes[absent]; ok {
			t.Fatalf("record reported %s: %#v", absent, attributes)
		}
	}
	if attributes["auth.mode"] != "none" || attributes["auth.outcome"] != "recovery_not_run" {
		t.Fatalf("record attributes = %#v", attributes)
	}
}
