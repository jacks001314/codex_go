package telemetry

import (
	"context"
	"testing"
	"time"
)

// Rust's session-start record: the model, permission, and auth-environment
// settings reach both records, while only the log record carries the MCP server
// names and only the trace event carries their count.
func TestEmitConversationStartsRoutesLogAndTraceLikeRust(t *testing.T) {
	t.Setenv(OpenAIAPIKeyEnvVar, "sk-test")
	t.Setenv(CodexAPIKeyEnvVar, "")
	t.Setenv("CUSTOM_PROVIDER_KEY", "")
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
	span := tracesClient.Tracer().StartSpan("session", nil)
	EmitConversationStarts(WithSpan(context.Background(), span), session, ConversationStartsEvent{
		ProviderName:          "OpenAI",
		ReasoningEffort:       "high",
		ReasoningSummary:      "auto",
		ContextWindow:         int64Ptr(272000),
		AutoCompactTokenLimit: int64Ptr(200000),
		ApprovalPolicy:        "on-request",
		SandboxPolicy:         "workspace-write",
		MCPServers:            []string{"docs", "calendar"},
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
			"event.name":                                  "codex.conversation_starts",
			"provider_name":                               "OpenAI",
			"auth.env_openai_api_key_present":             "true",
			"auth.env_codex_api_key_present":              "false",
			"auth.env_codex_api_key_enabled":              "false",
			"auth.env_provider_key_name":                  "configured",
			"auth.env_provider_key_present":               "false",
			"auth.env_refresh_token_url_override_present": "true",
			"reasoning_effort":                            "high",
			"reasoning_summary":                           "auto",
			"context_window":                              "272000",
			"auto_compact_token_limit":                    "200000",
			"approval_policy":                             "on-request",
			"sandbox_policy":                              "workspace-write",
			"mcp_servers":                                 "calendar, docs",
		} {
			if got := attributes[key]; got != want {
				t.Fatalf("log attribute %s = %q, want %q", key, got, want)
			}
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the logs endpoint did not receive the session-start record")
	}

	select {
	case body := <-traceBodies:
		spans := body["resourceSpans"].([]any)[0].(map[string]any)["scopeSpans"].([]any)[0].(map[string]any)["spans"].([]any)
		events := spans[0].(map[string]any)["events"].([]any)
		if len(events) != 1 {
			t.Fatalf("span events = %#v", events)
		}
		attributes := spanEventAttributes(t, events[0].(map[string]any))
		if attributes["provider_name"] != "OpenAI" || attributes["mcp_server_count"] != "2" {
			t.Fatalf("span event attributes = %#v", attributes)
		}
		if attributes["approval_policy"] != "on-request" || attributes["sandbox_policy"] != "workspace-write" {
			t.Fatalf("span event attributes = %#v", attributes)
		}
		// The trace event never carries the server names themselves.
		if _, ok := attributes["mcp_servers"]; ok {
			t.Fatalf("trace event leaked the MCP server names: %#v", attributes)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the traces endpoint did not receive the session-start event")
	}
}

// The optional settings are absent from the record when the session has none,
// because tracing records nothing for an absent Option.
func TestEmitConversationStartsOmitsUnsetOptionals(t *testing.T) {
	t.Setenv(OpenAIAPIKeyEnvVar, "")
	t.Setenv(CodexAPIKeyEnvVar, "")
	t.Setenv(RefreshTokenURLOverrideEnvVar, "")

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
	EmitConversationStarts(context.Background(), session, ConversationStartsEvent{
		ProviderName:     "OpenAI",
		ReasoningSummary: "auto",
		ApprovalPolicy:   "never",
		SandboxPolicy:    "read-only",
	})
	if err := logsClient.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	body := <-logBodies
	_, record := singleLogRecord(t, body)
	attributes := logRecordAttributes(t, record)
	for _, absent := range []string{"reasoning_effort", "context_window", "auto_compact_token_limit", "auth.env_provider_key_name"} {
		if _, ok := attributes[absent]; ok {
			t.Fatalf("record reported %s: %#v", absent, attributes)
		}
	}
	// The MCP server list is always present, empty when nothing is configured.
	if attributes["mcp_servers"] != "" {
		t.Fatalf("mcp_servers = %q", attributes["mcp_servers"])
	}
}

func int64Ptr(value int64) *int64 {
	return &value
}
