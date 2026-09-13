package telemetry

import (
	"context"
	"testing"
	"time"

	"codex_go/model"
)

// The handshake record: a successful dial reports no status, and a failed one
// reports the status, the error, and the response's request id / cf-ray on both
// records.
func TestRecordWebsocketConnectRoutesLogAndTraceLikeRust(t *testing.T) {
	t.Setenv(OpenAIAPIKeyEnvVar, "sk-test")

	logBodies := make(chan map[string]any, 2)
	logServer := newLogBatchServer(t, logBodies)
	defer logServer.Close()
	traceBodies := make(chan map[string]any, 2)
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
		AuthEnv:        CollectAuthEnvTelemetry("CUSTOM_PROVIDER_KEY", false),
	})
	session.Logs = logsClient
	span := tracesClient.Tracer().StartSpan("stream_request", nil)
	session.RecordWebsocketConnect(WithSpan(context.Background(), span), model.WebsocketConnectRecord{
		Duration:           17 * time.Millisecond,
		Endpoint:           "/responses",
		AuthHeaderAttached: true,
		AuthHeaderName:     "authorization",
		AgentID:            "agent-runtime-ws",
		TaskID:             "task-run-ws",
	})
	status := 401
	session.RecordWebsocketConnect(WithSpan(context.Background(), span), model.WebsocketConnectRecord{
		Duration:               12 * time.Millisecond,
		Status:                 &status,
		ErrorMessage:           "handshake failed: HTTP 401",
		Endpoint:               "/responses",
		AuthHeaderAttached:     true,
		AuthHeaderName:         "authorization",
		RetryAfterUnauthorized: true,
		RequestID:              "req-ws-401",
		CFRay:                  "ray-ws-401",
		AuthError:              "missing_authorization_header",
		AuthErrorCode:          "token_expired",
	})
	span.End()
	if err := logsClient.Flush(context.Background()); err != nil {
		t.Fatalf("logs Flush() error = %v", err)
	}
	if err := tracesClient.Flush(context.Background()); err != nil {
		t.Fatalf("traces Flush() error = %v", err)
	}

	select {
	case body := <-logBodies:
		records := logRecords(t, body)
		if len(records) != 2 {
			t.Fatalf("records = %#v", records)
		}
		success := logRecordAttributes(t, records[0])
		for key, want := range map[string]string{
			"event.name":                      "codex.websocket_connect",
			"duration_ms":                     "17",
			"success":                         "true",
			"endpoint":                        "/responses",
			"auth.header_attached":            "true",
			"auth.header_name":                "authorization",
			"auth.connection_reused":          "false",
			"auth.agent_id":                   "agent-runtime-ws",
			"auth.task_id":                    "task-run-ws",
			"auth.env_openai_api_key_present": "true",
		} {
			if got := success[key]; got != want {
				t.Fatalf("log attribute %s = %q, want %q", key, got, want)
			}
		}
		for _, absent := range []string{"http.response.status_code", "error.message", "auth.request_id"} {
			if _, ok := success[absent]; ok {
				t.Fatalf("successful handshake reported %s: %#v", absent, success)
			}
		}
		failure := logRecordAttributes(t, records[1])
		for key, want := range map[string]string{
			"success":                       "false",
			"http.response.status_code":     "401",
			"error.message":                 "handshake failed: HTTP 401",
			"auth.retry_after_unauthorized": "true",
			"auth.request_id":               "req-ws-401",
			"auth.cf_ray":                   "ray-ws-401",
			"auth.error":                    "missing_authorization_header",
			"auth.error_code":               "token_expired",
		} {
			if got := failure[key]; got != want {
				t.Fatalf("failure attribute %s = %q, want %q", key, got, want)
			}
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the logs endpoint did not receive the connect records")
	}

	select {
	case body := <-traceBodies:
		spans := body["resourceSpans"].([]any)[0].(map[string]any)["scopeSpans"].([]any)[0].(map[string]any)["spans"].([]any)
		events := spans[0].(map[string]any)["events"].([]any)
		if len(events) != 2 {
			t.Fatalf("span events = %#v", events)
		}
		attributes := spanEventAttributes(t, events[1].(map[string]any))
		if attributes["event.name"] != "codex.websocket_connect" || attributes["http.response.status_code"] != "401" {
			t.Fatalf("span event attributes = %#v", attributes)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the traces endpoint did not receive the connect events")
	}
}

// The completed-response record carries the token counts, the time to first
// token, and the inference settings on both records, and omits the counts a
// response did not report.
func TestRecordSSEEventCompletedRoutesLogAndTraceLikeRust(t *testing.T) {
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
	span := tracesClient.Tracer().StartSpan("handle_responses", nil)
	ttft := int64(137)
	session.RecordSSEEventCompleted(WithSpan(context.Background(), span), model.SSECompletedRecord{
		Usage: model.AgentUsage{
			InputTokens:           7,
			CachedInputTokens:     2,
			CacheWriteInputTokens: 4,
			OutputTokens:          3,
			ReasoningOutputTokens: 1,
			TotalTokens:           10,
		},
		TTFTMillis:      &ttft,
		ServiceTier:     "priority",
		ReasoningEffort: "high",
	})
	span.End()
	if err := logsClient.Flush(context.Background()); err != nil {
		t.Fatalf("logs Flush() error = %v", err)
	}
	if err := tracesClient.Flush(context.Background()); err != nil {
		t.Fatalf("traces Flush() error = %v", err)
	}

	want := map[string]string{
		"event.name":              "codex.sse_event",
		"event.kind":              "response.completed",
		"input_token_count":       "7",
		"output_token_count":      "3",
		"tool_token_count":        "10",
		"cached_token_count":      "2",
		"cache_write_token_count": "4",
		"reasoning_token_count":   "1",
		"ttft_ms":                 "137",
		"service_tier":            "priority",
		"model_reasoning_effort":  "high",
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
		t.Fatal("the logs endpoint did not receive the completed record")
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
		t.Fatal("the traces endpoint did not receive the completed event")
	}
}

// A response that reported no cache or reasoning counts leaves those fields
// absent, and a stream that never reported an output item has no ttft.
func TestRecordSSEEventCompletedOmitsUnsetCounts(t *testing.T) {
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
	session.RecordSSEEventCompleted(context.Background(), model.SSECompletedRecord{
		Usage: model.AgentUsage{InputTokens: 7, OutputTokens: 3, TotalTokens: 10},
	})
	if err := logsClient.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	body := <-logBodies
	_, record := singleLogRecord(t, body)
	attributes := logRecordAttributes(t, record)
	for _, absent := range []string{"cached_token_count", "cache_write_token_count", "reasoning_token_count", "ttft_ms", "service_tier", "model_reasoning_effort"} {
		if _, ok := attributes[absent]; ok {
			t.Fatalf("record reported %s: %#v", absent, attributes)
		}
	}
	if attributes["input_token_count"] != "7" || attributes["tool_token_count"] != "10" {
		t.Fatalf("record attributes = %#v", attributes)
	}
}

// Rust's websocket request record: the outcome, the auth environment, the
// reused connection, and the agent identity reach both records.
func TestRecordWebsocketRequestRoutesLogAndTraceLikeRust(t *testing.T) {
	t.Setenv(OpenAIAPIKeyEnvVar, "sk-test")

	logBodies := make(chan map[string]any, 2)
	logServer := newLogBatchServer(t, logBodies)
	defer logServer.Close()
	traceBodies := make(chan map[string]any, 2)
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
		AuthEnv:        CollectAuthEnvTelemetry("", false),
	})
	session.Logs = logsClient
	span := tracesClient.Tracer().StartSpan("stream_request", nil)
	session.RecordWebsocketRequest(WithSpan(context.Background(), span), model.WebsocketRequestRecord{
		Duration:         17 * time.Millisecond,
		ConnectionReused: true,
		AgentID:          "agent-runtime-ws",
		TaskID:           "task-run-ws",
	})
	session.RecordWebsocketRequest(WithSpan(context.Background(), span), model.WebsocketRequestRecord{
		Duration:     5 * time.Millisecond,
		ErrorMessage: "send failed",
	})
	span.End()
	if err := logsClient.Flush(context.Background()); err != nil {
		t.Fatalf("logs Flush() error = %v", err)
	}
	if err := tracesClient.Flush(context.Background()); err != nil {
		t.Fatalf("traces Flush() error = %v", err)
	}

	select {
	case body := <-logBodies:
		records := logRecords(t, body)
		if len(records) != 2 {
			t.Fatalf("records = %#v", records)
		}
		success := logRecordAttributes(t, records[0])
		for key, want := range map[string]string{
			"event.name":                      "codex.websocket_request",
			"duration_ms":                     "17",
			"success":                         "true",
			"auth.connection_reused":          "true",
			"auth.agent_id":                   "agent-runtime-ws",
			"auth.task_id":                    "task-run-ws",
			"auth.env_openai_api_key_present": "true",
		} {
			if got := success[key]; got != want {
				t.Fatalf("log attribute %s = %q, want %q", key, got, want)
			}
		}
		if _, ok := success["error.message"]; ok {
			t.Fatalf("a successful send recorded an error: %#v", success)
		}
		failure := logRecordAttributes(t, records[1])
		if failure["success"] != "false" || failure["error.message"] != "send failed" {
			t.Fatalf("failure record = %#v", failure)
		}
		if failure["auth.connection_reused"] != "false" {
			t.Fatalf("failure record = %#v", failure)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the logs endpoint did not receive the websocket records")
	}

	select {
	case body := <-traceBodies:
		spans := body["resourceSpans"].([]any)[0].(map[string]any)["scopeSpans"].([]any)[0].(map[string]any)["spans"].([]any)
		events := spans[0].(map[string]any)["events"].([]any)
		if len(events) != 2 {
			t.Fatalf("span events = %#v", events)
		}
		attributes := spanEventAttributes(t, events[0].(map[string]any))
		if attributes["event.name"] != "codex.websocket_request" || attributes["auth.connection_reused"] != "true" {
			t.Fatalf("span event attributes = %#v", attributes)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the traces endpoint did not receive the websocket events")
	}
}

// logRecords flattens every log record of a captured batch.
func logRecords(t *testing.T, batch map[string]any) []map[string]any {
	t.Helper()
	records := []map[string]any{}
	for _, resourceEntry := range batch["resourceLogs"].([]any) {
		for _, scopeEntry := range resourceEntry.(map[string]any)["scopeLogs"].([]any) {
			for _, recordEntry := range scopeEntry.(map[string]any)["logRecords"].([]any) {
				record, ok := recordEntry.(map[string]any)
				if ok {
					records = append(records, record)
				}
			}
		}
	}
	return records
}

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
