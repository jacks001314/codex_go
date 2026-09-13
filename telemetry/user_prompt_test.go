package telemetry

import (
	"context"
	"testing"
	"time"
)

// Rust's otel_export_routing_policy test for user prompts: the log record
// carries the prompt text only when the session logs user prompts, and the
// trace-safe event carries the counts without the text.
func TestEmitUserPromptRoutesLogAndTraceLikeRust(t *testing.T) {
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
		AccountEmail:   "engineer@example.com",
		LogUserPrompts: true,
	})
	session.Logs = logsClient
	span := tracesClient.Tracer().StartSpan("run_turn", nil)
	EmitUserPrompt(WithSpan(context.Background(), span), session, []UserPromptInput{
		{Kind: UserPromptText, Text: "super secret prompt"},
		{Kind: UserPromptImage},
		{Kind: UserPromptLocalImage},
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
		if attributes["event.name"] != "codex.user_prompt" || attributes["prompt"] != "super secret prompt" {
			t.Fatalf("log attributes = %#v", attributes)
		}
		// Rust counts characters, not bytes.
		if attributes["prompt_length"] != "19" {
			t.Fatalf("prompt_length = %q", attributes["prompt_length"])
		}
		if attributes["user.email"] != "engineer@example.com" {
			t.Fatalf("log attributes = %#v", attributes)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the logs endpoint did not receive the prompt record")
	}

	select {
	case body := <-traceBodies:
		spans := body["resourceSpans"].([]any)[0].(map[string]any)["scopeSpans"].([]any)[0].(map[string]any)["spans"].([]any)
		events := spans[0].(map[string]any)["events"].([]any)
		if len(events) != 1 {
			t.Fatalf("span events = %#v", events)
		}
		attributes := spanEventAttributes(t, events[0].(map[string]any))
		for key, want := range map[string]string{
			"event.name":              "codex.user_prompt",
			"prompt_length":           "19",
			"text_input_count":        "1",
			"image_input_count":       "1",
			"local_image_input_count": "1",
		} {
			if got := attributes[key]; got != want {
				t.Fatalf("span event attribute %s = %q, want %q", key, got, want)
			}
		}
		for _, absent := range []string{"prompt", "user.email", "user.account_id"} {
			if _, ok := attributes[absent]; ok {
				t.Fatalf("trace event leaked %s: %#v", absent, attributes)
			}
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the traces endpoint did not receive the prompt event")
	}
}

// Without the log-user-prompts setting the log record carries the redaction
// marker, and a multi-byte prompt still reports its character count.
func TestEmitUserPromptRedactsWithoutTheSetting(t *testing.T) {
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
	EmitUserPrompt(context.Background(), session, []UserPromptInput{{Kind: UserPromptText, Text: "héllo"}})
	if err := logsClient.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	body := <-logBodies
	_, record := singleLogRecord(t, body)
	attributes := logRecordAttributes(t, record)
	if attributes["prompt"] != "[REDACTED]" || attributes["prompt_length"] != "5" {
		t.Fatalf("log attributes = %#v", attributes)
	}
}
