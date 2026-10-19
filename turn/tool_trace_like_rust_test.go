package turn

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/model"
	"codex_go/tool"
)

// milestoneTracer records Rust #44661's trace-safe events and handler spans.
type milestoneTracer struct {
	mu     sync.Mutex
	events []milestoneEvent
	spans  []*milestoneSpan
}

type milestoneEvent struct {
	name   string
	fields map[string]string
}

type milestoneSpan struct {
	name    string
	attrs   map[string]string
	records []map[string]string
	ended   bool
}

func (t *milestoneTracer) StartSpan(ctx context.Context, _ model.TelemetrySpan, name string, attributes map[string]string) (context.Context, model.TelemetrySpan) {
	span := &milestoneSpan{name: name, attrs: cloneTraceStringMap(attributes)}
	t.mu.Lock()
	t.spans = append(t.spans, span)
	t.mu.Unlock()
	return ctx, span
}

func (t *milestoneTracer) EmitTraceSafeEvent(_ context.Context, eventName string, fields map[string]string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.events = append(t.events, milestoneEvent{name: eventName, fields: cloneTraceStringMap(fields)})
}

func (s *milestoneSpan) End() {
	s.ended = true
}

func (s *milestoneSpan) Record(attributes map[string]string) {
	s.records = append(s.records, cloneTraceStringMap(attributes))
}

func (s *milestoneSpan) SetName(name string) {
	s.name = name
}

func (s *milestoneSpan) TraceContext() (string, string, bool) {
	return "", "", false
}

func cloneTraceStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func milestoneEvents(tracer *milestoneTracer, name string) []milestoneEvent {
	tracer.mu.Lock()
	defer tracer.mu.Unlock()
	var matched []milestoneEvent
	for _, event := range tracer.events {
		if event.name == name {
			matched = append(matched, event)
		}
	}
	return matched
}

func milestoneSpanByName(tracer *milestoneTracer, name string) *milestoneSpan {
	tracer.mu.Lock()
	defer tracer.mu.Unlock()
	for _, span := range tracer.spans {
		if span.name == name {
			return span
		}
	}
	return nil
}

func TestToolDispatcherTracesReceiptAndReadinessLikeRust(t *testing.T) {
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("echo")}, func(context.Context, *tool.Invocation) (*tool.Output, error) {
		return &tool.Output{Success: true, Body: "secret output"}, nil
	})); err != nil {
		t.Fatal(err)
	}
	tracer := &milestoneTracer{}
	dispatcher := NewToolDispatcher(&ToolDispatcherOptions{
		Router:   tool.NewRouter(registry),
		ThreadID: "thread-trace",
		TurnID:   "turn-trace",
		Tracer:   tracer,
	})

	if _, err := dispatcher.ExecuteToolItems(context.Background(), []model.AgentItem{{
		Type:      "function_call",
		Name:      "echo",
		CallID:    "call-trace",
		Arguments: `{"text":"secret input"}`,
	}}); err != nil {
		t.Fatal(err)
	}

	received := milestoneEvents(tracer, ToolCallReceivedEventName)
	ready := milestoneEvents(tracer, ToolResultReadyEventName)
	if len(received) != 1 || len(ready) != 1 {
		t.Fatalf("received=%#v ready=%#v", received, ready)
	}
	want := map[string]string{
		"conversation.id": "thread-trace",
		"turn_id":         "turn-trace",
		"call_id":         "call-trace",
		"tool_name":       "echo",
		"tool_namespace":  tool.DefaultFunctionNamespace,
		"tool_source":     ToolTraceSourceDirect,
	}
	for key, value := range want {
		if got := received[0].fields[key]; got != value {
			t.Fatalf("received[%s] = %q, want %q (%#v)", key, got, value, received[0].fields)
		}
		if got := ready[0].fields[key]; got != value {
			t.Fatalf("ready[%s] = %q, want %q (%#v)", key, got, value, ready[0].fields)
		}
	}
	for _, event := range append(received, ready...) {
		for key, value := range event.fields {
			if strings.Contains(value, "secret") {
				t.Fatalf("%s leaked payload in %s=%q", event.name, key, value)
			}
		}
	}
}

func TestToolDispatcherTracesCodeModeHandlerOutcomeLikeRust(t *testing.T) {
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName(tool.CodeModeExecToolName)}, func(context.Context, *tool.Invocation) (*tool.Output, error) {
		return &tool.Output{Success: true, Data: map[string]any{"cell_id": "cell-1", "running": true}}, nil
	})); err != nil {
		t.Fatal(err)
	}
	tracer := &milestoneTracer{}
	dispatcher := NewToolDispatcher(&ToolDispatcherOptions{
		Router:   tool.NewRouter(registry),
		ThreadID: "thread-trace",
		TurnID:   "turn-trace",
		Tracer:   tracer,
	})

	if _, err := dispatcher.ExecuteToolItems(context.Background(), []model.AgentItem{{
		Type:   "custom_tool_call",
		Name:   tool.CodeModeExecToolName,
		CallID: "call-exec",
		Input:  `text("hi")`,
	}}); err != nil {
		t.Fatal(err)
	}

	span := milestoneSpanByName(tracer, CodeModeExecuteHandlerSpanName)
	if span == nil {
		t.Fatal("missing code-mode execute handler span")
	}
	if !span.ended {
		t.Fatal("handler span was not ended")
	}
	if span.attrs["conversation.id"] != "thread-trace" || span.attrs["turn_id"] != "turn-trace" || span.attrs["call_id"] != "call-exec" {
		t.Fatalf("handler span attributes = %#v", span.attrs)
	}
	if len(span.records) == 0 || span.records[len(span.records)-1]["outcome"] != "completed" || span.records[len(span.records)-1]["cell.id"] != "cell-1" {
		t.Fatalf("handler outcome records = %#v", span.records)
	}
}

func TestToolDispatcherTracesNestedCodeModeDispatchLikeRust(t *testing.T) {
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName(tool.CodeModeExecToolName)}, func(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
		nested := &tool.Invocation{
			CallID:   "runtime-1",
			ToolName: tool.PlainName("read_file"),
			Context:  map[string]any{tool.CodeModeCellIDContextKey: "cell-1"},
		}
		now := time.Now().UTC()
		if started, ok := invocation.Context["code_mode_nested_tool_started"].(tool.CodeModeNestedToolStartedFunc); ok {
			started(ctx, nested, now)
		}
		if completed, ok := invocation.Context["code_mode_nested_tool_completed"].(tool.CodeModeNestedToolCompletedFunc); ok {
			completed(ctx, nested, &tool.Output{Success: true, Body: "ok"}, nil, now, now.Add(time.Millisecond))
		}
		return &tool.Output{Success: true, Data: map[string]any{"cell_id": "cell-1", "running": true}}, nil
	})); err != nil {
		t.Fatal(err)
	}
	tracer := &milestoneTracer{}
	dispatcher := NewToolDispatcher(&ToolDispatcherOptions{
		Router:   tool.NewRouter(registry),
		ThreadID: "thread-trace",
		TurnID:   "turn-trace",
		Tracer:   tracer,
	})

	if _, err := dispatcher.ExecuteToolItems(context.Background(), []model.AgentItem{{
		Type:   "custom_tool_call",
		Name:   tool.CodeModeExecToolName,
		CallID: "call-exec",
		Input:  `text("hi")`,
	}}); err != nil {
		t.Fatal(err)
	}

	dispatched := milestoneEvents(tracer, CodeModeNestedToolDispatchedEventName)
	if len(dispatched) != 1 {
		t.Fatalf("nested dispatch events = %#v", dispatched)
	}
	if dispatched[0].fields["cell.id"] != "cell-1" || dispatched[0].fields["runtime_tool_call_id"] != "runtime-1" {
		t.Fatalf("nested dispatch fields = %#v", dispatched[0].fields)
	}
	codeModeReceipts := 0
	codeModeReady := 0
	for _, event := range milestoneEvents(tracer, ToolCallReceivedEventName) {
		if event.fields["tool_source"] == ToolTraceSourceCodeMode && event.fields["tool_name"] == "read_file" {
			codeModeReceipts++
		}
	}
	for _, event := range milestoneEvents(tracer, ToolResultReadyEventName) {
		if event.fields["tool_source"] == ToolTraceSourceCodeMode && event.fields["call_id"] == "runtime-1" {
			if event.fields["turn_id"] != "turn-trace" {
				t.Fatalf("code-mode readiness turn_id = %q", event.fields["turn_id"])
			}
			codeModeReady++
		}
	}
	if codeModeReceipts != 1 || codeModeReady != 1 {
		t.Fatalf("code-mode receipts=%d ready=%d", codeModeReceipts, codeModeReady)
	}
}
