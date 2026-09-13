package telemetry

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"codex_go/protocol"
)

// Rust's otel_export_routing_policy test for tool results: the log record
// carries the arguments, the truncated output preview, and the agent name,
// while the trace-safe event carries only the lengths and the origin fields.
func TestEmitToolResultRoutesLogAndTraceLikeRust(t *testing.T) {
	resetToolResultSequence()
	defer resetToolResultSequence()

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
	telemetry := NewSessionTelemetry(SessionTelemetryMetadata{
		ConversationID: "thread-1",
		AgentName:      "/root/reviewer",
		AuthMode:       "apikey",
		Originator:     "codex_cli_rs",
		Model:          "gpt-5.1",
		Slug:           "gpt-5.1",
		AppVersion:     "0.1.0",
		TerminalType:   "tty",
	})
	telemetry.Logs = logsClient
	span := tracesClient.Tracer().StartSpan("run_turn", nil)
	output := strings.Repeat("secret output\nsecond line\n", 100)
	EmitToolResult(WithSpan(context.Background(), span), telemetry,
		protocol.ToolResultLogConfig{MaxBytes: len(output)}, ToolResultEvent{
			ToolName:        "shell",
			ToolNamespace:   "mcp__example",
			CallID:          "call-1",
			Arguments:       "secret arguments",
			MCPServer:       "internal-mcp",
			MCPServerOrigin: "stdio",
			Duration:        42 * time.Millisecond,
			Success:         true,
			Output:          output,
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
		scope, record := singleLogRecord(t, body)
		if scope != OtelLogOnlyTarget {
			t.Fatalf("scope = %q", scope)
		}
		if _, ok := record["body"]; ok {
			t.Fatalf("a message-less event must not carry a body: %#v", record["body"])
		}
		attributes := logRecordAttributes(t, record)
		for key, want := range map[string]string{
			"event.name":        ToolResultEventName,
			"tool_name":         "shell",
			"tool_namespace":    "mcp__example",
			"call_id":           "call-1",
			"duration_ms":       "42",
			"success":           "true",
			"output_truncated":  "false",
			"tool_result_seq":   "1",
			"conversation.id":   "thread-1",
			"agent_name":        "/root/reviewer",
			"arguments":         "secret arguments",
			"output":            output,
			"mcp_server":        "internal-mcp",
			"mcp_server_origin": "stdio",
			"model":             "gpt-5.1",
			"originator":        "codex_cli_rs",
		} {
			if got := attributes[key]; got != want {
				t.Fatalf("log attribute %s = %q, want %q", key, got, want)
			}
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the logs endpoint did not receive the record")
	}

	select {
	case body := <-traceBodies:
		spans := body["resourceSpans"].([]any)[0].(map[string]any)["scopeSpans"].([]any)[0].(map[string]any)["spans"].([]any)
		events := spans[0].(map[string]any)["events"].([]any)
		if len(events) != 1 {
			t.Fatalf("span events = %#v", events)
		}
		event := events[0].(map[string]any)
		if event["name"] != ToolResultEventName {
			t.Fatalf("event name = %#v", event["name"])
		}
		attributes := spanEventAttributes(t, event)
		for key, want := range map[string]string{
			"event.name":        ToolResultEventName,
			"level":             "INFO",
			"target":            OtelTraceSafeTarget,
			"tool_name":         "shell",
			"tool_namespace":    "mcp__example",
			"tool_result_seq":   "1",
			"output_truncated":  "false",
			"arguments_length":  "16",
			"output_length":     "2600",
			"output_line_count": "200",
			"tool_origin":       "mcp",
			"mcp_tool":          "true",
		} {
			if got := attributes[key]; got != want {
				t.Fatalf("span event attribute %s = %q, want %q", key, got, want)
			}
		}
		for _, absent := range []string{"arguments", "output", "agent_name", "mcp_server", "mcp_server_origin"} {
			if _, ok := attributes[absent]; ok {
				t.Fatalf("trace event leaked %s: %#v", absent, attributes)
			}
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the traces endpoint did not receive the span")
	}
}

// The log record's output is truncated to the configured byte budget, and the
// trace event reports the untruncated length (Rust's telemetry_preview).
func TestEmitToolResultTruncatesOnlyTheLogPreview(t *testing.T) {
	resetToolResultSequence()
	defer resetToolResultSequence()

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
	output := strings.Repeat("secret output\n", 100)
	span := tracesClient.Tracer().StartSpan("run_turn", nil)
	telemetry := NewSessionTelemetry(SessionTelemetryMetadata{
		ConversationID: "thread-1",
		AppVersion:     "0.1.0",
	})
	telemetry.Logs = logsClient
	EmitToolResult(WithSpan(context.Background(), span), telemetry, protocol.ToolResultLogConfig{MaxBytes: 32}, ToolResultEvent{
		ToolName: "shell",
		CallID:   "call-2",
		Output:   output,
	})
	span.End()
	if err := logsClient.Flush(context.Background()); err != nil {
		t.Fatalf("logs Flush() error = %v", err)
	}
	if err := tracesClient.Flush(context.Background()); err != nil {
		t.Fatalf("traces Flush() error = %v", err)
	}

	body := <-logBodies
	_, record := singleLogRecord(t, body)
	attributes := logRecordAttributes(t, record)
	if attributes["output_truncated"] != "true" {
		t.Fatalf("log attributes = %#v", attributes)
	}
	if !strings.HasSuffix(attributes["output"], ToolResultTruncationNotice) {
		t.Fatalf("output preview = %q", attributes["output"])
	}
	if !strings.Contains(attributes["output"], "[... telemetry preview truncated ...]") {
		t.Fatalf("output preview = %q", attributes["output"])
	}

	traceBody := <-traceBodies
	spans := traceBody["resourceSpans"].([]any)[0].(map[string]any)["scopeSpans"].([]any)[0].(map[string]any)["spans"].([]any)
	events := spans[0].(map[string]any)["events"].([]any)
	attributes = spanEventAttributes(t, events[0].(map[string]any))
	if attributes["output_length"] != "1400" || attributes["output_truncated"] != "true" {
		t.Fatalf("span event attributes = %#v", attributes)
	}
}

// A trace-safe record outside a span is dropped, like Rust's tracing layer
// ignoring an event that has no enclosing span.
func TestTraceEventWithoutSpanIsDroppedLikeRust(t *testing.T) {
	traceBodies := make(chan map[string]any, 1)
	traceServer := newTraceBatchServer(t, traceBodies)
	defer traceServer.Close()
	tracesClient := NewTracesClient(TracesClientOptions{
		ServiceName:    "codex-app-server",
		Endpoint:       traceServer.URL + "/v1/traces",
		ExportInterval: -1,
	})
	telemetry := NewSessionTelemetry(SessionTelemetryMetadata{ConversationID: "thread-1"})
	telemetry.TraceEvent(context.Background(), TelemetryEvent{Name: "codex.api_request"})
	if err := tracesClient.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	select {
	case body := <-traceBodies:
		t.Fatalf("exported a trace event without a span: %#v", body)
	case <-time.After(100 * time.Millisecond):
	}
}

// The records omit the optional identity fields the session does not have,
// because tracing records nothing for an absent Option.
func TestSessionTelemetryOmitsAbsentIdentityFields(t *testing.T) {
	var batch map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		payload := map[string]any{}
		_ = json.NewDecoder(request.Body).Decode(&payload)
		batch = payload
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	logsClient := NewLogsClient(LogsClientOptions{
		ServiceName:    "codex-app-server",
		Endpoint:       server.URL + "/v1/logs",
		ExportInterval: -1,
	})
	telemetry := NewSessionTelemetry(SessionTelemetryMetadata{
		ConversationID: "thread-1",
		Originator:     "codex_cli_rs",
		AppVersion:     "0.1.0",
		TerminalType:   "tty",
	})
	telemetry.Logs = logsClient
	telemetry.LogEvent(context.Background(), TelemetryEvent{Name: "codex.api_request"})
	if err := logsClient.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	_, record := singleLogRecord(t, batch)
	attributes := logRecordAttributes(t, record)
	for _, absent := range []string{"auth_mode", "user.account_id", "user.email"} {
		if _, ok := attributes[absent]; ok {
			t.Fatalf("record exported %s: %#v", absent, attributes)
		}
	}
	if attributes["event.name"] != "codex.api_request" || attributes["conversation.id"] != "thread-1" {
		t.Fatalf("record attributes = %#v", attributes)
	}
	if _, ok := record["body"]; ok {
		t.Fatalf("record body = %#v", record["body"])
	}
}

// newLogBatchServer captures the first OTLP/HTTP log export it receives.
func newLogBatchServer(t *testing.T, bodies chan<- map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		payload, _ := io.ReadAll(request.Body)
		var body map[string]any
		if err := json.Unmarshal(payload, &body); err != nil {
			t.Errorf("log batch json error = %v", err)
		}
		select {
		case bodies <- body:
		default:
		}
		writer.WriteHeader(http.StatusOK)
	}))
}

// singleLogRecord returns the one scope name and log record of a captured batch.
func singleLogRecord(t *testing.T, batch map[string]any) (string, map[string]any) {
	t.Helper()
	resourceLogs, _ := batch["resourceLogs"].([]any)
	if len(resourceLogs) != 1 {
		t.Fatalf("resource logs = %#v", resourceLogs)
	}
	scopeLogs, _ := resourceLogs[0].(map[string]any)["scopeLogs"].([]any)
	if len(scopeLogs) != 1 {
		t.Fatalf("scope logs = %#v", scopeLogs)
	}
	scoped, _ := scopeLogs[0].(map[string]any)
	records, _ := scoped["logRecords"].([]any)
	if len(records) != 1 {
		t.Fatalf("log records = %#v", records)
	}
	record, _ := records[0].(map[string]any)
	scope, _ := scoped["scope"].(map[string]any)
	name, _ := scope["name"].(string)
	return name, record
}

func logRecordAttributes(t *testing.T, record map[string]any) map[string]string {
	t.Helper()
	attributes := map[string]string{}
	entries, _ := record["attributes"].([]any)
	for _, entry := range entries {
		attribute, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		value, _ := attribute["value"].(map[string]any)
		text, _ := value["stringValue"].(string)
		attributes[attribute["key"].(string)] = text
	}
	return attributes
}

func spanEventAttributes(t *testing.T, event map[string]any) map[string]string {
	t.Helper()
	attributes := map[string]string{}
	entries, _ := event["attributes"].([]any)
	for _, entry := range entries {
		attribute, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		value, _ := attribute["value"].(map[string]any)
		text, _ := value["stringValue"].(string)
		attributes[attribute["key"].(string)] = text
	}
	return attributes
}
