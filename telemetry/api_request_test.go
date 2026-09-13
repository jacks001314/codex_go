package telemetry

import (
	"context"
	"testing"
	"time"

	"codex_go/model"
)

// Rust's otel_export_routing_policy test for API requests: the attempt, status,
// endpoint, and auth observability reach both the log record and the trace
// event, and the trace event never gained anything the log record lacks.
func TestRecordAPIRequestRoutesLogAndTraceLikeRust(t *testing.T) {
	t.Setenv(OpenAIAPIKeyEnvVar, "sk-test")
	t.Setenv(CodexAPIKeyEnvVar, "")
	t.Setenv(RefreshTokenURLOverrideEnvVar, "https://example.test/token")

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
	session := NewSessionTelemetry(SessionTelemetryMetadata{
		ConversationID: "thread-1",
		AppVersion:     "0.1.0",
		Model:          "gpt-5.1",
		AuthEnv:        CollectAuthEnvTelemetry("CUSTOM_PROVIDER_KEY", false),
	})
	session.Logs = logsClient
	status := 401
	span := tracesClient.Tracer().StartSpan("stream_request", nil)
	session.RecordAPIRequest(WithSpan(context.Background(), span), model.APIRequestRecord{
		Attempt:                1,
		Duration:               42 * time.Millisecond,
		Status:                 &status,
		ErrorMessage:           "http 401",
		Endpoint:               "/responses",
		AuthHeaderAttached:     true,
		AuthHeaderName:         "authorization",
		RetryAfterUnauthorized: true,
		RecoveryMode:           "managed",
		RecoveryPhase:          "refresh_token",
		RequestID:              "req-401",
		CFRay:                  "ray-401",
		AuthError:              "missing_authorization_header",
		AuthErrorCode:          "token_expired",
		AgentID:                "agent-runtime",
		TaskID:                 "task-run",
	})
	span.End()
	if err := logsClient.Flush(context.Background()); err != nil {
		t.Fatalf("logs Flush() error = %v", err)
	}
	if err := tracesClient.Flush(context.Background()); err != nil {
		t.Fatalf("traces Flush() error = %v", err)
	}

	want := map[string]string{
		"event.name":                                  "codex.api_request",
		"duration_ms":                                 "42",
		"attempt":                                     "1",
		"http.response.status_code":                   "401",
		"error.message":                               "http 401",
		"endpoint":                                    "/responses",
		"auth.header_attached":                        "true",
		"auth.header_name":                            "authorization",
		"auth.retry_after_unauthorized":               "true",
		"auth.recovery_mode":                          "managed",
		"auth.recovery_phase":                         "refresh_token",
		"auth.request_id":                             "req-401",
		"auth.cf_ray":                                 "ray-401",
		"auth.error":                                  "missing_authorization_header",
		"auth.error_code":                             "token_expired",
		"auth.agent_id":                               "agent-runtime",
		"auth.task_id":                                "task-run",
		"auth.env_openai_api_key_present":             "true",
		"auth.env_codex_api_key_present":              "false",
		"auth.env_codex_api_key_enabled":              "false",
		"auth.env_provider_key_name":                  "configured",
		"auth.env_refresh_token_url_override_present": "true",
		"conversation.id":                             "thread-1",
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
		t.Fatal("the logs endpoint did not receive the api-request record")
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
		t.Fatal("the traces endpoint did not receive the api-request event")
	}
}

// A transport failure reports no status and keeps the auth fields the attempt
// observed; the recovery context is absent when the attempt was not a retry.
func TestRecordAPIRequestOmitsAbsentFieldsLikeRust(t *testing.T) {
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
	session.RecordAPIRequest(context.Background(), model.APIRequestRecord{
		Attempt:  0,
		Duration: 5 * time.Millisecond,
		Endpoint: "/responses",
	})
	if err := logsClient.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	body := <-logBodies
	_, record := singleLogRecord(t, body)
	attributes := logRecordAttributes(t, record)
	for _, absent := range []string{
		"http.response.status_code", "error.message", "auth.header_name",
		"auth.recovery_mode", "auth.recovery_phase", "auth.request_id",
		"auth.cf_ray", "auth.error", "auth.error_code", "auth.agent_id", "auth.task_id",
	} {
		if _, ok := attributes[absent]; ok {
			t.Fatalf("record reported %s: %#v", absent, attributes)
		}
	}
	if attributes["auth.header_attached"] != "false" || attributes["attempt"] != "0" {
		t.Fatalf("record attributes = %#v", attributes)
	}
}
