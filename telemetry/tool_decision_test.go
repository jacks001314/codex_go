package telemetry

import (
	"context"
	"testing"
	"time"
)

// The decision record is log-only: it names the tool, its namespace (defaulting
// to `functions`), the call, and the opaque decision, and reports the source
// only when one made the decision.
func TestEmitToolDecisionIsLogOnlyLikeRust(t *testing.T) {
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
	span := tracesClient.Tracer().StartSpan("run_turn", nil)
	EmitToolDecision(WithSpan(context.Background(), span), session, ToolDecisionEvent{
		ToolName: "shell",
		CallID:   "call-1",
		Decision: ToolDecisionDenied,
		Source:   ToolDecisionSourceUser,
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
		_, record := singleLogRecord(t, body)
		attributes := logRecordAttributes(t, record)
		for key, want := range map[string]string{
			"event.name":      "codex.tool_decision",
			"tool_name":       "shell",
			"tool_namespace":  "functions",
			"call_id":         "call-1",
			"decision":        "denied",
			"source":          "user",
			"conversation.id": "thread-1",
		} {
			if got := attributes[key]; got != want {
				t.Fatalf("log attribute %s = %q, want %q", key, got, want)
			}
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the logs endpoint did not receive the decision record")
	}

	// Rust logs this event without a trace half, so the span stays event-free.
	select {
	case body := <-traceBodies:
		spans := body["resourceSpans"].([]any)[0].(map[string]any)["scopeSpans"].([]any)[0].(map[string]any)["spans"].([]any)
		if events, ok := spans[0].(map[string]any)["events"].([]any); ok && len(events) > 0 {
			t.Fatalf("span events = %#v", events)
		}
	case <-time.After(200 * time.Millisecond):
	}
}

// A decision from no tracked source omits the source field, and a namespaced
// tool reports its namespace.
func TestEmitToolDecisionOmitsAbsentSourceLikeRust(t *testing.T) {
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
	EmitToolDecision(context.Background(), session, ToolDecisionEvent{
		ToolName:      "shell",
		ToolNamespace: "mcp__example",
		CallID:        "call-2",
		Decision:      ToolDecisionApproved,
	})
	if err := logsClient.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	body := <-logBodies
	_, record := singleLogRecord(t, body)
	attributes := logRecordAttributes(t, record)
	if _, ok := attributes["source"]; ok {
		t.Fatalf("record reported a source: %#v", attributes)
	}
	if attributes["tool_namespace"] != "mcp__example" || attributes["decision"] != "approved" {
		t.Fatalf("record attributes = %#v", attributes)
	}
}
