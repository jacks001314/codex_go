package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/model"
	"codex_go/session"
	"codex_go/state"
	"codex_go/telemetry"
	"codex_go/tool"
	"codex_go/turn"
)

// The app-server emits the tool-result record pair beside the tool-call metrics:
// the diagnostic log record reaches a configured OTLP logs endpoint with the
// scope = target shape, and the trace-safe event lands on the span the call ran
// inside.
func TestToolResultRecordsReachThePipelines(t *testing.T) {
	logBodies := make(chan map[string]any, 1)
	traceBodies := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		payload := map[string]any{}
		_ = json.NewDecoder(request.Body).Decode(&payload)
		target := logBodies
		if strings.HasSuffix(request.URL.Path, "/v1/traces") {
			target = traceBodies
		}
		select {
		case target <- payload:
		default:
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	home := t.TempDir()
	configToml := fmt.Sprintf(
		"[otel.exporter.otlp-http]\nendpoint = %q\nprotocol = \"json\"\n\n[otel.trace_exporter.otlp-http]\nendpoint = %q\nprotocol = \"json\"\n",
		server.URL+"/v1/logs",
		server.URL+"/v1/traces",
	)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(configToml), 0o600); err != nil {
		t.Fatalf("WriteFile config.toml error = %v", err)
	}

	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home)})
	router.configureOtelMetrics(home, nil, state.NewTaskMetrics())
	provider := router.currentOtelProvider()
	if provider == nil || provider.Logs() == nil || provider.Tracer() == nil {
		t.Fatal("the OTLP provider was not built with both pipelines")
	}

	started := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	span := provider.Tracer().StartSpan("run_turn", nil)
	router.emitToolResultRecords(telemetry.WithSpan(context.Background(), span), "thread-1", &turn.ToolExecutionResult{
		Invocation: &tool.Invocation{
			CallID:   "call-1",
			ToolName: tool.NamespacedName("mcp__example", "shell"),
			Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"cmd":"ls"}`},
		},
		Output: &tool.Output{Success: true, Body: "out"},
		TelemetryTags: map[string]string{
			"mcp_server":        "internal-mcp",
			"mcp_server_origin": "stdio",
		},
		StartedAt:  started,
		FinishedAt: started.Add(42 * time.Millisecond),
	})
	span.End()
	if err := router.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	select {
	case payload := <-logBodies:
		scopeLogs := payload["resourceLogs"].([]any)[0].(map[string]any)["scopeLogs"].([]any)
		if len(scopeLogs) != 1 {
			t.Fatalf("scope logs = %#v", scopeLogs)
		}
		scoped := scopeLogs[0].(map[string]any)
		if name := scoped["scope"].(map[string]any)["name"]; name != "codex_otel.log_only" {
			t.Fatalf("scope = %#v", name)
		}
		records := scoped["logRecords"].([]any)
		if len(records) != 1 {
			t.Fatalf("log records = %#v", records)
		}
		record := records[0].(map[string]any)
		attributes := map[string]string{}
		for _, entry := range record["attributes"].([]any) {
			attribute := entry.(map[string]any)
			attributes[attribute["key"].(string)] = attribute["value"].(map[string]any)["stringValue"].(string)
		}
		for key, want := range map[string]string{
			"event.name":        "codex.tool_result",
			"tool_name":         "shell",
			"tool_namespace":    "mcp__example",
			"call_id":           "call-1",
			"duration_ms":       "42",
			"success":           "true",
			"output_truncated":  "false",
			"arguments":         `{"cmd":"ls"}`,
			"output":            "out",
			"mcp_server":        "internal-mcp",
			"mcp_server_origin": "stdio",
			"conversation.id":   "thread-1",
		} {
			if got := attributes[key]; got != want {
				t.Fatalf("log attribute %s = %q, want %q", key, got, want)
			}
		}
		if attributes["app.version"] == "" {
			t.Fatalf("log record has no app.version: %#v", attributes)
		}
		if _, ok := record["body"]; ok {
			t.Fatalf("record body = %#v", record["body"])
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the OTLP logs endpoint did not receive the tool-result record")
	}

	select {
	case payload := <-traceBodies:
		spans := payload["resourceSpans"].([]any)[0].(map[string]any)["scopeSpans"].([]any)[0].(map[string]any)["spans"].([]any)
		if len(spans) != 1 || spans[0].(map[string]any)["name"] != "run_turn" {
			t.Fatalf("spans = %#v", spans)
		}
		events := spans[0].(map[string]any)["events"].([]any)
		if len(events) != 1 {
			t.Fatalf("span events = %#v", events)
		}
		event := events[0].(map[string]any)
		if event["name"] != "codex.tool_result" {
			t.Fatalf("span event = %#v", event)
		}
		attributes := map[string]string{}
		for _, entry := range event["attributes"].([]any) {
			attribute := entry.(map[string]any)
			attributes[attribute["key"].(string)] = attribute["value"].(map[string]any)["stringValue"].(string)
		}
		for key, want := range map[string]string{
			"event.name":        "codex.tool_result",
			"target":            "codex_otel.trace_safe",
			"tool_name":         "shell",
			"tool_namespace":    "mcp__example",
			"arguments_length":  "12",
			"output_length":     "3",
			"output_line_count": "1",
			"tool_origin":       "mcp",
			"mcp_tool":          "true",
		} {
			if got := attributes[key]; got != want {
				t.Fatalf("span event attribute %s = %q, want %q", key, got, want)
			}
		}
		if _, ok := attributes["arguments"]; ok {
			t.Fatalf("span event leaked the arguments: %#v", attributes)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the OTLP traces endpoint did not receive the span")
	}
}

// The session metadata carries the conversation identity and the model the
// session runs, and falls back to the root agent path like Rust's agent_name
// metadata.
func TestSessionTelemetryMetadataForThread(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	metadata := router.sessionTelemetryMetadataForThread("thread-1")
	if metadata.ConversationID != "thread-1" {
		t.Fatalf("metadata = %#v", metadata)
	}
	if metadata.AppVersion == "" {
		t.Fatalf("metadata = %#v", metadata)
	}
	if metadata.AgentName != "/root" {
		t.Fatalf("agent name = %q", metadata.AgentName)
	}
	if metadata.Slug != metadata.Model {
		t.Fatalf("slug = %q model = %q", metadata.Slug, metadata.Model)
	}
}

// encodedAttributes flattens the string attributes of one encoded span or span
// event.
func encodedAttributes(container map[string]any) map[string]string {
	attributes := map[string]string{}
	entries, _ := container["attributes"].([]any)
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

// A turn's model request runs inside Rust's `run_sampling_request` span, which
// carries the turn id, the model, and the working directory.
func TestTurnStartOpensSamplingRequestSpanLikeRust(t *testing.T) {
	traceBodies := make(chan map[string]any, 4)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		payload := map[string]any{}
		_ = json.NewDecoder(request.Body).Decode(&payload)
		select {
		case traceBodies <- payload:
		default:
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	provider, err := telemetry.NewOtelProvider(telemetry.OtelSettings{
		ServiceName:   otelAppServerServiceName,
		TraceExporter: telemetry.OtelExporter{Kind: telemetry.OtelExporterOtlpHTTP, Endpoint: server.URL + "/v1/traces"},
	})
	if err != nil || provider == nil {
		t.Fatalf("provider = %#v error = %v", provider, err)
	}
	home := t.TempDir()
	store := session.NewStore(filepath.Join(home, "sessions"))
	sink := NewNotificationBuffer()
	agent := newRecordingRuntimeAgent("ok")
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
		Config:       config.NewConfigService(home),
	})
	router.SetNotificationSink(sink)
	router.installOtelProvider(provider, state.NewTaskMetrics())
	defer router.Close()

	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: home}))
	if start.Error != nil {
		t.Fatalf("thread start error: %+v", start.Error)
	}
	threadID := start.Result.(*ThreadStartResponse).Thread.ID
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "inspect",
		CWD:      home,
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)
	if err := provider.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	found := false
	for {
		select {
		case payload := <-traceBodies:
			spans := payload["resourceSpans"].([]any)[0].(map[string]any)["scopeSpans"].([]any)[0].(map[string]any)["spans"].([]any)
			for _, entry := range spans {
				span := entry.(map[string]any)
				if span["name"] != "run_sampling_request" {
					continue
				}
				attributes := encodedAttributes(span)
				if attributes["turn_id"] != turnID {
					t.Fatalf("span attributes = %#v, want turn %q", attributes, turnID)
				}
				if attributes["model"] == "" || attributes["cwd"] == "" {
					t.Fatalf("span attributes = %#v", attributes)
				}
				found = true
			}
		case <-time.After(500 * time.Millisecond):
			if !found {
				t.Fatal("the exported spans do not include the sampling-request span")
			}
			return
		}
	}
}

// stubMCPExecutor registers one MCP tool so the router reports its server tags.
type stubMCPExecutor struct {
	name       tool.ToolName
	server     string
	serverType string
}

func (e stubMCPExecutor) Spec() tool.Spec { return tool.Spec{Name: e.name} }

func (e stubMCPExecutor) Execute(context.Context, *tool.Invocation) (*tool.Output, error) {
	return &tool.Output{Success: true}, nil
}

func (e stubMCPExecutor) TelemetryTags(*tool.Invocation) map[string]string {
	return map[string]string{"mcp_server": e.server, "mcp_server_origin": e.serverType}
}

// An MCP tool call is bracketed by Rust's `mcp.tools.call` span (client kind,
// rpc/tool/server attributes), and the call's trace-safe records attach to it.
func TestMCPToolCallSpanLikeRust(t *testing.T) {
	traceBodies := make(chan map[string]any, 4)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		payload := map[string]any{}
		_ = json.NewDecoder(request.Body).Decode(&payload)
		select {
		case traceBodies <- payload:
		default:
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	provider, err := telemetry.NewOtelProvider(telemetry.OtelSettings{
		ServiceName:   otelAppServerServiceName,
		TraceExporter: telemetry.OtelExporter{Kind: telemetry.OtelExporterOtlpHTTP, Endpoint: server.URL + "/v1/traces"},
	})
	if err != nil || provider == nil {
		t.Fatalf("provider = %#v error = %v", provider, err)
	}
	registry := tool.NewRegistry()
	toolName := tool.NamespacedName("mcp__example", "shell")
	if err := registry.Register(stubMCPExecutor{name: toolName, server: "example", serverType: "stdio"}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ToolRouter: tool.NewRouter(registry)})
	router.installOtelProvider(provider, state.NewTaskMetrics())
	defer router.Close()

	invocation := &tool.Invocation{CallID: "call-1", ToolName: toolName}
	started := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	router.runtimeToolStartedNotifier("thread-1", "turn-1", "", false)(context.Background(), invocation, started)
	router.runtimeToolCompletedNotifier("thread-1", "turn-1", "", false)(context.Background(), &turn.ToolExecutionResult{
		Invocation: invocation,
		Output:     &tool.Output{Success: true, Body: "out"},
		TelemetryTags: map[string]string{
			"mcp_server":        "example",
			"mcp_server_origin": "stdio",
		},
		StartedAt:  started,
		FinishedAt: started.Add(25 * time.Millisecond),
	})
	if err := provider.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	select {
	case payload := <-traceBodies:
		spans := payload["resourceSpans"].([]any)[0].(map[string]any)["scopeSpans"].([]any)[0].(map[string]any)["spans"].([]any)
		var call map[string]any
		for _, entry := range spans {
			span := entry.(map[string]any)
			if span["name"] == "mcp.tools.call" {
				call = span
			}
		}
		if call == nil {
			t.Fatalf("spans = %#v", spans)
		}
		if call["kind"] != float64(telemetry.SpanKindClient) {
			t.Fatalf("span kind = %#v", call["kind"])
		}
		attributes := encodedAttributes(call)
		for key, want := range map[string]string{
			"rpc.system":        "jsonrpc",
			"rpc.method":        "tools/call",
			"mcp.server.name":   "example",
			"mcp.server.origin": "stdio",
			"mcp.transport":     "stdio",
			"tool.name":         "shell",
			"tool.call_id":      "call-1",
			"conversation.id":   "thread-1",
			"session.id":        "thread-1",
			"turn.id":           "turn-1",
		} {
			if got := attributes[key]; got != want {
				t.Fatalf("span attribute %s = %q, want %q", key, got, want)
			}
		}
		for _, connectorKey := range []string{"mcp.connector.id", "mcp.connector.name"} {
			if _, ok := attributes[connectorKey]; !ok {
				t.Fatalf("span attributes = %#v", attributes)
			}
		}
		// The call's tool-result record lands on this span (Rust instruments the
		// call body, so the record's trace half attaches to it).
		events, _ := call["events"].([]any)
		if len(events) != 1 {
			t.Fatalf("span events = %#v", events)
		}
		eventAttributes := encodedAttributes(events[0].(map[string]any))
		if eventAttributes["event.name"] != "codex.tool_result" || eventAttributes["tool_name"] != "shell" {
			t.Fatalf("span event attributes = %#v", eventAttributes)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the traces endpoint did not receive the MCP call span")
	}
}

// An approval resolution reports who decided what: the Guardian-resolved
// approvals carry the automated-reviewer source and the opaque decision
// (Rust's SessionTelemetry::tool_decision via approvals::record_resolution).
func TestApprovalDecisionsReportToolDecisionLikeRust(t *testing.T) {
	received := make(chan map[string]any, 8)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		payload := map[string]any{}
		_ = json.NewDecoder(request.Body).Decode(&payload)
		select {
		case received <- payload:
		default:
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	home := t.TempDir()
	cwd := t.TempDir()
	configBody := fmt.Sprintf("model = \"gpt-5.4\"\napprovals_reviewer = \"auto_review\"\n\n[otel.exporter.otlp-http]\nendpoint = %q\nprotocol = \"json\"\n",
		server.URL+"/v1/logs")
	if err := os.WriteFile(config.ConfigPath(home), []byte(configBody), 0o600); err != nil {
		t.Fatalf("WriteFile config error = %v", err)
	}
	decisions := []state.ReviewDecision{state.DecisionApproved, state.DecisionDenied, state.DecisionAborted}
	reasons := []string{"", "too destructive", "turn aborted"}
	for index, decision := range decisions {
		router := NewRuntimeRouter(RuntimeServices{
			DefaultCWD:       cwd,
			Config:           config.NewConfigService(home),
			GuardianReviewer: &fakeGuardianReviewer{decision: decision, reason: reasons[index]},
		})
		router.configureOtelMetrics(home, nil, state.NewTaskMetrics())
		params := &turn.TurnStartParams{ThreadID: "thread-1", CWD: cwd, Model: "gpt-5.4"}
		if err := router.threads.RegisterTurn("thread-1", "turn-1", nil, 0, params); err != nil {
			t.Fatalf("RegisterTurn() error = %v", err)
		}
		approval := router.shellApprovalForTurn("thread-1", "turn-1", false)
		_, _ = approval(context.Background(), &tool.ShellApprovalRequest{
			Request: &tool.ShellRequest{HookCommand: "rm -rf build", CWD: cwd},
			Invocation: &tool.Invocation{
				CallID:   fmt.Sprintf("call-%d", index+1),
				ToolName: tool.PlainName("shell"),
			},
		})
		if err := router.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	}

	want := []map[string]string{
		{"decision": "approved", "source": "automated_reviewer", "call_id": "call-1"},
		{"decision": "denied", "source": "automated_reviewer", "call_id": "call-2"},
		{"decision": "abort", "source": "automated_reviewer", "call_id": "call-3"},
	}
	records := []map[string]string{}
	deadline := time.After(3 * time.Second)
	for len(records) < len(want) {
		select {
		case payload := <-received:
			for _, attributes := range toolDecisionRecords(t, payload) {
				records = append(records, attributes)
			}
		case <-deadline:
			t.Fatalf("records = %#v, want %d", records, len(want))
		}
	}
	for index, expected := range want {
		attributes := records[index]
		for key, value := range expected {
			if got := attributes[key]; got != value {
				t.Fatalf("record %d attribute %s = %q, want %q", index, key, got, value)
			}
		}
		if attributes["tool_name"] != "shell" || attributes["tool_namespace"] != "functions" {
			t.Fatalf("record %d = %#v", index, attributes)
		}
		if attributes["conversation.id"] != "thread-1" {
			t.Fatalf("record %d = %#v", index, attributes)
		}
	}
}

// toolDecisionRecords returns the codex.tool_decision records of one captured
// OTLP logs payload.
func toolDecisionRecords(t *testing.T, payload map[string]any) []map[string]string {
	t.Helper()
	records := []map[string]string{}
	resourceLogs, _ := payload["resourceLogs"].([]any)
	for _, resourceEntry := range resourceLogs {
		scopeLogs, _ := resourceEntry.(map[string]any)["scopeLogs"].([]any)
		for _, scopeEntry := range scopeLogs {
			logRecords, _ := scopeEntry.(map[string]any)["logRecords"].([]any)
			for _, recordEntry := range logRecords {
				record, ok := recordEntry.(map[string]any)
				if !ok {
					continue
				}
				attributes := map[string]string{}
				for _, entry := range record["attributes"].([]any) {
					attribute := entry.(map[string]any)
					attributes[attribute["key"].(string)] = attribute["value"].(map[string]any)["stringValue"].(string)
				}
				if attributes["event.name"] == "codex.tool_decision" {
					records = append(records, attributes)
				}
			}
		}
	}
	return records
}

// The user's approval answers map onto Rust's opaque decision strings.
func TestApprovalDecisionStringsLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		decision any
		want     string
	}{
		{CommandExecutionApprovalAccept, "approved"},
		{CommandExecutionApprovalAcceptForSession, "approved_for_session"},
		{CommandExecutionApprovalAcceptWithExecpolicyAmendment, "approved_with_amendment"},
		{CommandExecutionApprovalDecline, "denied"},
		{CommandExecutionApprovalCancel, "abort"},
		{map[string]any{"applyNetworkPolicyAmendment": map[string]any{"network_policy_amendment": map[string]any{"action": "allow"}}}, "approved_with_network_policy_allow"},
		{map[string]any{"applyNetworkPolicyAmendment": map[string]any{"network_policy_amendment": map[string]any{"action": "deny"}}}, "denied_with_network_policy_deny"},
		{"unknown", ""},
	} {
		if got := commandExecutionApprovalToolDecision(testCase.decision); got != testCase.want {
			t.Fatalf("command decision %#v = %q, want %q", testCase.decision, got, testCase.want)
		}
	}
	for _, testCase := range []struct {
		decision any
		want     string
	}{
		{FileChangeApprovalAccept, "approved"},
		{FileChangeApprovalAcceptForSession, "approved_for_session"},
		{FileChangeApprovalDecline, "denied"},
		{FileChangeApprovalCancel, "abort"},
		{"unknown", ""},
	} {
		if got := fileChangeApprovalToolDecision(testCase.decision); got != testCase.want {
			t.Fatalf("file change decision %#v = %q, want %q", testCase.decision, got, testCase.want)
		}
	}
}

// The request span is reachable while its request is dispatched, and its W3C
// trace context is what a started turn carries into the model request (Rust's
// OutgoingMessageSender::register_request_context +
// TurnInputRequest::with_trace).
func TestRequestSpanTraceFollowsTheTurnLikeRust(t *testing.T) {
	traceBodies := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		payload := map[string]any{}
		_ = json.NewDecoder(request.Body).Decode(&payload)
		select {
		case traceBodies <- payload:
		default:
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	provider, err := telemetry.NewOtelProvider(telemetry.OtelSettings{
		ServiceName:   otelAppServerServiceName,
		TraceExporter: telemetry.OtelExporter{Kind: telemetry.OtelExporterOtlpHTTP, Endpoint: server.URL + "/v1/traces"},
		Tracestate:    map[string]map[string]string{"example": {"alpha": "one"}},
	})
	if err != nil || provider == nil || provider.Tracer() == nil {
		t.Fatalf("provider = %#v error = %v", provider, err)
	}
	router := NewRuntimeRouter(RuntimeServices{})
	router.installOtelProvider(provider, state.NewTaskMetrics())
	defer router.Close()

	request := requestWithParams(t, IntID(7), MethodThreadList, ThreadListParams{})
	request.ConnectionID = "conn-1"
	span := startRequestSpan(router.requestTracer(), request, "stdio")
	if span == nil {
		t.Fatal("the request span was not started")
	}
	router.registerRequestSpan(request, span)
	trace := router.requestSpanTrace(request)
	router.unregisterRequestSpan(request)
	span.End()
	if trace == nil {
		t.Fatal("the request span produced no trace context")
	}
	if !strings.HasPrefix(trace.Traceparent, "00-") || len(trace.Traceparent) != 55 {
		t.Fatalf("traceparent = %q", trace.Traceparent)
	}
	if trace.Tracestate != "example=alpha:one" {
		t.Fatalf("tracestate = %q", trace.Tracestate)
	}
	if got := router.requestSpanTrace(request); got != nil {
		t.Fatalf("an unregistered request still reports a trace: %#v", got)
	}

	if err := provider.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	select {
	case payload := <-traceBodies:
		spans := payload["resourceSpans"].([]any)[0].(map[string]any)["scopeSpans"].([]any)[0].(map[string]any)["spans"].([]any)
		if len(spans) != 1 {
			t.Fatalf("spans = %#v", spans)
		}
		// The carrier the turn carries names the very span the exporter received.
		if !strings.Contains(trace.Traceparent, spans[0].(map[string]any)["spanId"].(string)) {
			t.Fatalf("traceparent = %q span = %#v", trace.Traceparent, spans[0])
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the traces endpoint did not receive the request span")
	}
}

// A thread start reports the session-start telemetry: the provider, model and
// permission settings, the auth environment, and the enabled MCP servers
// (Rust's SessionTelemetry::conversation_starts, emitted when the core session
// starts).
func TestThreadStartEmitsConversationStartsLikeRust(t *testing.T) {
	received := make(chan map[string]any, 8)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		payload := map[string]any{}
		_ = json.NewDecoder(request.Body).Decode(&payload)
		select {
		case received <- payload:
		default:
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	t.Setenv("OPENAI_API_KEY", "sk-test")
	home := t.TempDir()
	configToml := fmt.Sprintf(`[otel.exporter.otlp-http]
endpoint = %q
protocol = "json"

[mcp_servers.docs]
command = "npx"

[mcp_servers.disabled_server]
command = "npx"
enabled = false
`, server.URL+"/v1/logs")
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(configToml), 0o600); err != nil {
		t.Fatalf("WriteFile config.toml error = %v", err)
	}

	store := session.NewStore(filepath.Join(home, "sessions"))
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
	})
	router.configureOtelMetrics(home, nil, state.NewTaskMetrics())
	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: home}))
	if start.Error != nil {
		t.Fatalf("thread start error: %+v", start.Error)
	}
	threadID := start.Result.(*ThreadStartResponse).Thread.ID
	if err := router.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	deadline := time.After(3 * time.Second)
	for {
		select {
		case payload := <-received:
			resourceLogs, _ := payload["resourceLogs"].([]any)
			if len(resourceLogs) == 0 {
				continue
			}
			for _, scopeEntry := range resourceLogs[0].(map[string]any)["scopeLogs"].([]any) {
				for _, recordEntry := range scopeEntry.(map[string]any)["logRecords"].([]any) {
					record := recordEntry.(map[string]any)
					attributes := map[string]string{}
					for _, entry := range record["attributes"].([]any) {
						attribute := entry.(map[string]any)
						attributes[attribute["key"].(string)] = attribute["value"].(map[string]any)["stringValue"].(string)
					}
					if attributes["event.name"] != "codex.conversation_starts" {
						continue
					}
					if attributes["conversation.id"] != threadID {
						t.Fatalf("conversation.id = %q, want %q", attributes["conversation.id"], threadID)
					}
					if attributes["provider_name"] == "" {
						t.Fatalf("record = %#v", attributes)
					}
					// Only the enabled MCP servers are reported.
					if attributes["mcp_servers"] != "docs" {
						t.Fatalf("mcp_servers = %q", attributes["mcp_servers"])
					}
					if attributes["reasoning_summary"] != "auto" {
						t.Fatalf("reasoning_summary = %q", attributes["reasoning_summary"])
					}
					if attributes["auth.env_openai_api_key_present"] != "true" ||
						attributes["auth.env_codex_api_key_enabled"] != "false" {
						t.Fatalf("auth env = %#v", attributes)
					}
					if attributes["sandbox_policy"] == "" || attributes["approval_policy"] == "" {
						t.Fatalf("permission settings = %#v", attributes)
					}
					return
				}
			}
		case <-deadline:
			t.Fatal("the OTLP logs endpoint did not receive the session-start record")
		}
	}
}

// A turn start reports its user prompt through the logs pipeline, and the
// `otel.log_user_prompt` setting decides whether the text or the redaction
// marker reaches the record (Rust's SessionTelemetry::user_prompt).
func TestTurnStartEmitsUserPromptRecordLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		configOtel string
		wantPrompt string
	}{
		{name: "logs the prompt", configOtel: "[otel]\nlog_user_prompt = true\n", wantPrompt: "secret prompt"},
		{name: "redacts the prompt", configOtel: "", wantPrompt: "[REDACTED]"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			received := make(chan map[string]any, 8)
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				payload := map[string]any{}
				_ = json.NewDecoder(request.Body).Decode(&payload)
				select {
				case received <- payload:
				default:
				}
				writer.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			home := t.TempDir()
			configToml := fmt.Sprintf("%s[otel.exporter.otlp-http]\nendpoint = %q\nprotocol = \"json\"\n",
				testCase.configOtel, server.URL+"/v1/logs")
			if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(configToml), 0o600); err != nil {
				t.Fatalf("WriteFile config.toml error = %v", err)
			}
			store := session.NewStore(filepath.Join(home, "sessions"))
			sink := NewNotificationBuffer()
			agent := newRecordingRuntimeAgent("ok")
			router := NewRuntimeRouter(RuntimeServices{
				ThreadRouter: NewRouter(store),
				Turns:        turn.NewTurnService(),
				Agent:        agent,
				ThreadStatus: NewThreadStatusManager(),
				Config:       config.NewConfigService(home),
			})
			router.SetNotificationSink(sink)
			router.configureOtelMetrics(home, nil, state.NewTaskMetrics())
			defer router.Close()

			start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: home}))
			if start.Error != nil {
				t.Fatalf("thread start error: %+v", start.Error)
			}
			threadID := start.Result.(*ThreadStartResponse).Thread.ID
			turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
				ThreadID: threadID,
				Prompt:   "secret prompt",
				Input:    []turn.TurnUserInput{{Type: "image", URL: "data:image/png;base64,iVBORw0KGgo="}},
			}))
			if turnStart.Error != nil {
				t.Fatalf("turn start error: %+v", turnStart.Error)
			}
			turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID
			waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)
			if err := router.Close(); err != nil {
				t.Fatalf("Close() error = %v", err)
			}

			deadline := time.After(3 * time.Second)
			for {
				select {
				case payload := <-received:
					resourceLogs, _ := payload["resourceLogs"].([]any)
					if len(resourceLogs) == 0 {
						continue
					}
					for _, scopeEntry := range resourceLogs[0].(map[string]any)["scopeLogs"].([]any) {
						for _, recordEntry := range scopeEntry.(map[string]any)["logRecords"].([]any) {
							record := recordEntry.(map[string]any)
							attributes := map[string]string{}
							for _, entry := range record["attributes"].([]any) {
								attribute := entry.(map[string]any)
								attributes[attribute["key"].(string)] = attribute["value"].(map[string]any)["stringValue"].(string)
							}
							if attributes["event.name"] != "codex.user_prompt" {
								continue
							}
							if attributes["prompt"] != testCase.wantPrompt {
								t.Fatalf("prompt = %q, want %q", attributes["prompt"], testCase.wantPrompt)
							}
							// The prompt's rune length counts the text only, and the
							// image input is still counted separately.
							if attributes["prompt_length"] != "13" {
								t.Fatalf("prompt_length = %q", attributes["prompt_length"])
							}
							if attributes["conversation.id"] != threadID {
								t.Fatalf("conversation.id = %q", attributes["conversation.id"])
							}
							return
						}
					}
				case <-deadline:
					t.Fatal("the OTLP logs endpoint did not receive the prompt record")
				}
			}
		})
	}
}

// The `turn/start` request span's trace context follows the turn into the model
// request, so the exported span and the model call share one trace (Rust's
// OutgoingMessageSender::request_trace_context -> TurnInputRequest::with_trace).
func TestTurnStartTraceReachesTheModelRequestLikeRust(t *testing.T) {
	traceBodies := make(chan map[string]any, 4)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		payload := map[string]any{}
		_ = json.NewDecoder(request.Body).Decode(&payload)
		select {
		case traceBodies <- payload:
		default:
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	provider, err := telemetry.NewOtelProvider(telemetry.OtelSettings{
		ServiceName:   otelAppServerServiceName,
		TraceExporter: telemetry.OtelExporter{Kind: telemetry.OtelExporterOtlpHTTP, Endpoint: server.URL + "/v1/traces"},
	})
	if err != nil || provider == nil {
		t.Fatalf("provider = %#v error = %v", provider, err)
	}
	home := t.TempDir()
	store := session.NewStore(filepath.Join(home, "sessions"))
	sink := NewNotificationBuffer()
	agent := newRecordingRuntimeAgent("ok")
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
		Config:       config.NewConfigService(home),
	})
	router.SetNotificationSink(sink)
	router.installOtelProvider(provider, state.NewTaskMetrics())
	defer router.Close()

	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: home}))
	if start.Error != nil {
		t.Fatalf("thread start error: %+v", start.Error)
	}
	threadID := start.Result.(*ThreadStartResponse).Thread.ID
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "inspect",
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	agentRequest := waitForRuntimeAgentRequest(t, agent)
	if agentRequest.Trace == nil {
		t.Fatal("the model request did not carry the turn trace")
	}
	if !strings.HasPrefix(agentRequest.Trace.Traceparent, "00-") || len(agentRequest.Trace.Traceparent) != 55 {
		t.Fatalf("traceparent = %q", agentRequest.Trace.Traceparent)
	}
	if err := provider.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	// The carrier must name the exported `turn/start` request span, so the model
	// call continues the very trace the client started.
	carrierSpanID := strings.Split(agentRequest.Trace.Traceparent, "-")[2]
	found := false
	for {
		select {
		case payload := <-traceBodies:
			spans := payload["resourceSpans"].([]any)[0].(map[string]any)["scopeSpans"].([]any)[0].(map[string]any)["spans"].([]any)
			for _, entry := range spans {
				span := entry.(map[string]any)
				if span["name"] == string(MethodTurnStart) && span["spanId"] == carrierSpanID {
					found = true
				}
			}
		case <-time.After(500 * time.Millisecond):
			if !found {
				t.Fatalf("the exported spans do not carry the turn trace %q", agentRequest.Trace.Traceparent)
			}
			return
		}
	}
}

// The app-server binds the session telemetry to the model runner it builds, and
// the sink carries the provider's logs client so the client's records reach the
// log pipeline.
func TestInstallSessionTelemetryBindsTheLogsClient(t *testing.T) {
	home := t.TempDir()
	configToml := "[otel.exporter.otlp-http]\nendpoint = \"http://127.0.0.1:1/v1/logs\"\nprotocol = \"json\"\n\n" +
		"[otel.trace_exporter.otlp-http]\nendpoint = \"http://127.0.0.1:1/v1/traces\"\nprotocol = \"json\"\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(configToml), 0o600); err != nil {
		t.Fatalf("WriteFile config.toml error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home)})
	router.configureOtelMetrics(home, nil, state.NewTaskMetrics())
	defer router.Close()

	agent := &model.ResponsesAgentRunner{}
	router.installSessionTelemetry(agent, "thread-1")
	if agent.Telemetry == nil {
		t.Fatal("the model runner has no session telemetry sink")
	}
	session, ok := agent.Telemetry.(*telemetry.SessionTelemetry)
	if !ok {
		t.Fatalf("sink = %T", agent.Telemetry)
	}
	if session.Logs == nil {
		t.Fatal("the session telemetry has no logs client")
	}
	if session.Tracer == nil {
		t.Fatal("the session telemetry has no tracer for the client spans")
	}
	if session.Metadata.ConversationID != "thread-1" {
		t.Fatalf("metadata = %#v", session.Metadata)
	}
}

// A completed call without a payload logs an empty argument string, the way
// Rust's Function payload carries an empty arguments string when the model sent
// none.
func TestToolResultEventForExecutionDefaults(t *testing.T) {
	event := toolResultEventForExecution(&turn.ToolExecutionResult{
		Invocation: &tool.Invocation{ToolName: tool.PlainName("shell")},
	})
	if event.ToolName != "shell" || event.ToolNamespace != "" || event.Arguments != "" {
		t.Fatalf("event = %#v", event)
	}
	if event.Success {
		t.Fatalf("event = %#v", event)
	}

	// A direct plaintext collaboration call hides its arguments, like Rust's
	// tool_log_payload.
	plaintext := toolResultEventForExecution(&turn.ToolExecutionResult{
		Invocation: &tool.Invocation{
			ToolName: tool.NamespacedName("collaboration", "spawn_agent"),
			Source:   "direct_plaintext_message",
			Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: "secret"},
		},
	})
	if plaintext.Arguments != "[plaintext arguments]" {
		t.Fatalf("arguments = %q", plaintext.Arguments)
	}
}
