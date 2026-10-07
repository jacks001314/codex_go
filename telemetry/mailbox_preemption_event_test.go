package telemetry

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"codex_go/model"
	"codex_go/tool"
	"codex_go/turn"
)

// Rust #49262/#51249: the mailbox preemption record is trace-safe
// (`codex_otel.trace_safe`, INFO) and lands on the span the sampling request
// opened, which nests under the thread's span and carries the turn's ids.
func TestMailboxPreemptionTraceEventLandsOnSamplingSpanLikeRust(t *testing.T) {
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(payload, &body); err != nil {
			t.Errorf("span batch json error = %v payload=%s", err, payload)
		}
		bodies = append(bodies, body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	traces := NewTracesClient(TracesClientOptions{
		ServiceName:    "codex-app-server",
		Endpoint:       server.URL + "/v1/traces",
		ExportInterval: -1,
		Now:            func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	})
	if !traces.Enabled() {
		t.Fatal("traces client is disabled")
	}
	tracer := traces.Tracer()
	if tracer == nil {
		t.Fatal("tracer is nil")
	}
	session := tracer.StartSpan("session_loop", map[string]string{ThreadIDAttribute: "thread-1"})
	if session == nil {
		t.Fatal("session span is nil")
	}
	sessionTelemetry := NewSessionTelemetry(SessionTelemetryMetadata{
		ConversationID: "thread-1",
		AppVersion:     "test",
		Originator:     "codex_app_server",
	})
	sessionTelemetry.Tracer = tracer

	mailbox := turn.NewSteerMailbox()
	agent := &mailboxPreemptionTraceAgent{mailbox: mailbox}
	loop := turn.NewAgentLoop(&turn.AgentLoopOptions{Agent: agent, SteerMailbox: mailbox})
	if _, err := loop.Run(WithSpan(context.Background(), session), &turn.AgentLoopRequest{
		Prompt:   "plan the next action while the worker finishes",
		Model:    "gpt-5.4",
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		Tracer:   sessionTelemetry,
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	session.End()
	if err := traces.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if len(bodies) != 1 {
		t.Fatalf("span batches = %d, want 1", len(bodies))
	}
	spans := exportedSpans(t, bodies[0])
	sessionSpanID := ""
	for _, span := range spans {
		if span["name"] == "session_loop" {
			sessionSpanID, _ = span["spanId"].(string)
		}
	}
	if sessionSpanID == "" {
		t.Fatalf("thread span missing from the exported batch: %#v", spans)
	}
	events := 0
	for _, span := range spans {
		entries, _ := span["events"].([]any)
		for _, entry := range entries {
			event, ok := entry.(map[string]any)
			if !ok || event["name"] != "codex.mailbox_preemption" {
				continue
			}
			events++
			if span["name"] != turn.SamplingRequestSpanName {
				t.Fatalf("event landed on span %v, want %s", span["name"], turn.SamplingRequestSpanName)
			}
			if parent, _ := span["parentSpanId"].(string); parent != sessionSpanID {
				t.Fatalf("sampling span parent = %q, want the thread span %q", parent, sessionSpanID)
			}
			attributes := spanEventAttributes(t, event)
			for key, want := range map[string]string{
				"level":           "INFO",
				"target":          OtelTraceSafeTarget,
				"event.name":      "codex.mailbox_preemption",
				"conversation.id": "thread-1",
				"turn.id":         "turn-1",
			} {
				if got := attributes[key]; got != want {
					t.Fatalf("event attribute %q = %q, want %q (attributes=%v)", key, got, want, attributes)
				}
			}
		}
	}
	if events != 1 {
		t.Fatalf("mailbox preemption events = %d, want 1 (spans=%#v)", events, spans)
	}
}

func exportedSpans(t *testing.T, body map[string]any) []map[string]any {
	t.Helper()
	resourceSpans, _ := body["resourceSpans"].([]any)
	if len(resourceSpans) != 1 {
		t.Fatalf("resourceSpans = %#v", body["resourceSpans"])
	}
	scopeSpans, _ := resourceSpans[0].(map[string]any)["scopeSpans"].([]any)
	if len(scopeSpans) != 1 {
		t.Fatalf("scopeSpans = %#v", resourceSpans[0])
	}
	rawSpans, _ := scopeSpans[0].(map[string]any)["spans"].([]any)
	spans := make([]map[string]any, 0, len(rawSpans))
	for _, raw := range rawSpans {
		if span, ok := raw.(map[string]any); ok {
			spans = append(spans, span)
		}
	}
	if len(spans) == 0 {
		t.Fatalf("spans = %#v", scopeSpans[0])
	}
	return spans
}

// mailboxPreemptionTraceAgent queues inter-agent mail while its first sampling
// request is in flight, then answers the turn.
type mailboxPreemptionTraceAgent struct {
	mailbox  *turn.SteerMailbox
	requests []model.AgentRequest
}

func (a *mailboxPreemptionTraceAgent) Run(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	a.requests = append(a.requests, *request)
	if len(a.requests) == 1 {
		if err := a.mailbox.Enqueue(&turn.SteerEnqueueParams{
			ThreadID: request.ThreadID,
			TurnID:   request.TurnID,
			InputItems: []any{map[string]any{
				"type":      "agent_message",
				"author":    "/root/worker",
				"recipient": "/root",
				"content": []any{map[string]any{
					"type": "input_text",
					"text": "Message Type: MESSAGE\nTask name: /root\nSender: /root/worker\nPayload:\nWorker found the result.",
				}},
			}},
		}); err != nil {
			return nil, err
		}
		return &model.AgentResponse{
			ResponseID: "resp-boundary",
			Items: []model.AgentItem{
				{ID: "boundary-1", Type: "agent_message", Text: "I will update the plan.", Data: map[string]any{"phase": "commentary"}},
				{ID: "call-1", Type: "function_call", Name: "update_plan", CallID: "call-1", Arguments: `{}`},
			},
		}, nil
	}
	return &model.AgentResponse{
		ResponseID: "resp-answer",
		Message:    "The worker update is now included.",
		Items: []model.AgentItem{{
			ID: "final-1", Type: "agent_message", Text: "The worker update is now included.", Data: map[string]any{"phase": "final_answer"},
		}},
	}, nil
}

// phaseTraceToolAgent issues one tool call, then finishes the turn.
type phaseTraceToolAgent struct{ requests int }

func (a *phaseTraceToolAgent) Run(_ context.Context, _ *model.AgentRequest) (*model.AgentResponse, error) {
	a.requests++
	if a.requests == 1 {
		return &model.AgentResponse{
			ResponseID: "resp-tool",
			Items: []model.AgentItem{{
				ID: "call-1", Type: "function_call", Name: "echo", CallID: "call-1", Arguments: `{}`,
			}},
		}, nil
	}
	return &model.AgentResponse{
		ResponseID: "resp-final",
		Message:    "done",
		Items:      []model.AgentItem{{ID: "final-1", Type: "agent_message", Text: "done"}},
	}, nil
}

// Rust #49262: the turn loop's `codex.sampling` and `codex.tool_blocking` phase
// spans export through the session tracer with `codex.turn.phase` plus the
// conversation and turn ids, both nested under the sampling-request span.
func TestTurnPhaseSpansExportLikeRust(t *testing.T) {
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(payload, &body); err != nil {
			t.Errorf("span batch json error = %v payload=%s", err, payload)
		}
		bodies = append(bodies, body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	traces := NewTracesClient(TracesClientOptions{
		ServiceName:    "codex-app-server",
		Endpoint:       server.URL + "/v1/traces",
		ExportInterval: -1,
		Now:            func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	})
	if !traces.Enabled() {
		t.Fatal("traces client is disabled")
	}
	tracer := traces.Tracer()
	session := tracer.StartSpan("session_loop", map[string]string{ThreadIDAttribute: "thread-1"})
	if session == nil {
		t.Fatal("session span is nil")
	}
	sessionTelemetry := NewSessionTelemetry(SessionTelemetryMetadata{
		ConversationID: "thread-1",
		AppVersion:     "test",
		Originator:     "codex_app_server",
	})
	sessionTelemetry.Tracer = tracer

	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("echo")}, func(context.Context, *tool.Invocation) (*tool.Output, error) {
		return &tool.Output{Success: true, Body: "tool result"}, nil
	})); err != nil {
		t.Fatalf("register echo: %v", err)
	}
	executedToolCalls := turn.NewExecutedToolCallRecorder()
	loop := turn.NewAgentLoop(&turn.AgentLoopOptions{
		Agent:             &phaseTraceToolAgent{},
		Dispatcher:        turn.NewToolDispatcher(&turn.ToolDispatcherOptions{Router: tool.NewRouter(registry), ExecutedToolCalls: executedToolCalls}),
		ExecutedToolCalls: executedToolCalls,
		MaxTurns:          3,
	})
	if _, err := loop.Run(WithSpan(context.Background(), session), &turn.AgentLoopRequest{
		Prompt:   "run echo",
		Model:    "gpt-test",
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		Tracer:   sessionTelemetry,
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	session.End()
	if err := traces.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	var spans []map[string]any
	for _, body := range bodies {
		spans = append(spans, exportedSpans(t, body)...)
	}
	samplingRequestSpanIDs := map[string]bool{}
	for _, span := range spans {
		if span["name"] == turn.SamplingRequestSpanName {
			if id, _ := span["spanId"].(string); id != "" {
				samplingRequestSpanIDs[id] = true
			}
		}
	}
	if len(samplingRequestSpanIDs) == 0 {
		t.Fatalf("no %s span was exported (spans=%#v)", turn.SamplingRequestSpanName, spans)
	}
	phases := map[string]int{}
	for _, span := range spans {
		name, _ := span["name"].(string)
		if name != turn.SamplingPhaseSpanName && name != turn.ToolBlockingPhaseSpanName {
			continue
		}
		phases[name]++
		attributes := spanEventAttributes(t, map[string]any{"attributes": span["attributes"]})
		wantPhase := turn.TurnPhaseSampling
		if name == turn.ToolBlockingPhaseSpanName {
			wantPhase = turn.TurnPhaseToolBlocking
		}
		if attributes[turn.TurnPhaseAttribute] != wantPhase ||
			attributes[turn.ConversationIDAttribute] != "thread-1" ||
			attributes[turn.TurnIDAttribute] != "turn-1" {
			t.Fatalf("%s attributes = %#v", name, attributes)
		}
		if parent, _ := span["parentSpanId"].(string); !samplingRequestSpanIDs[parent] {
			t.Fatalf("%s parent = %q, want a sampling-request span %#v", name, parent, samplingRequestSpanIDs)
		}
	}
	// One sampling phase span per sampling request, and one tool-blocking span
	// for the single tool round.
	if phases[turn.SamplingPhaseSpanName] != 2 || phases[turn.ToolBlockingPhaseSpanName] != 1 {
		t.Fatalf("phase spans = %#v (spans=%#v)", phases, spans)
	}
}
