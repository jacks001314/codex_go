package turn

import (
	"context"
	"testing"

	"codex_go/model"
	"codex_go/protocol"
	"codex_go/tool"
)

// recordingSpanTracer captures the spans the turn loop opens.
type recordingSpanTracer struct {
	spans       []*recordingSpan
	traceparent string
}

type recordingSpan struct {
	name        string
	attributes  map[string]string
	parent      *recordingSpan
	context     context.Context
	traceparent string
	tracestate  string
	ended       bool
}

// recordingSpanKey carries a recorded span in its context, so a test can prove
// which span a callback ran under.
type recordingSpanKey struct{}

func (t *recordingSpanTracer) StartSpan(ctx context.Context, parent model.TelemetrySpan, name string, attributes map[string]string) (context.Context, model.TelemetrySpan) {
	span := &recordingSpan{name: name, attributes: attributes, context: ctx}
	span.traceparent = t.traceparent
	if concrete, ok := parent.(*recordingSpan); ok {
		span.parent = concrete
	}
	t.spans = append(t.spans, span)
	return context.WithValue(ctx, recordingSpanKey{}, span), span
}

// recordingSpanFromContext returns the span the recorder stored in ctx, or nil.
func recordingSpanFromContext(ctx context.Context) *recordingSpan {
	if ctx == nil {
		return nil
	}
	span, _ := ctx.Value(recordingSpanKey{}).(*recordingSpan)
	return span
}

func (s *recordingSpan) End()                     { s.ended = true }
func (s *recordingSpan) Record(map[string]string) {}
func (s *recordingSpan) SetName(string)           {}

func (s *recordingSpan) TraceContext() (string, string, bool) {
	return s.traceparent, s.tracestate, s.traceparent != ""
}

// The sampling span's W3C carrier follows the turn into tool dispatch, so
// anything that crosses a process boundary (the code-mode gRPC session)
// propagates the span context Rust reads from the current span.
func TestAgentLoopPropagatesSpanTraceContextLikeRust(t *testing.T) {
	tracer := &recordingSpanTracer{traceparent: "00-00000000000000000000000000000001-0000000000000002-01"}
	var seen *protocol.W3CTraceContext
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("echo")}, func(ctx context.Context, _ *tool.Invocation) (*tool.Output, error) {
		seen, _ = protocol.TraceContextFromContext(ctx)
		return &tool.Output{Success: true, Body: "tool result"}, nil
	})); err != nil {
		t.Fatalf("register echo: %v", err)
	}
	executedToolCalls := NewExecutedToolCallRecorder()
	loop := NewAgentLoop(&AgentLoopOptions{
		Agent:             &fakeLoopAgent{},
		Dispatcher:        NewToolDispatcher(&ToolDispatcherOptions{Router: tool.NewRouter(registry), ExecutedToolCalls: executedToolCalls}),
		ExecutedToolCalls: executedToolCalls,
		MaxTurns:          3,
	})
	if _, err := loop.Run(context.Background(), &AgentLoopRequest{
		Prompt:   "run echo",
		Model:    "gpt-test",
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		Tracer:   tracer,
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if seen == nil || seen.Traceparent != tracer.traceparent {
		t.Fatalf("tool dispatch trace = %#v", seen)
	}
}

// One `run_sampling_request` span is opened per sampling request with the turn's
// identity, mirroring Rust's instrumentation of the sampling request.
func TestAgentLoopOpensSamplingRequestSpanLikeRust(t *testing.T) {
	tracer := &recordingSpanTracer{}
	agent := &fakeLoopAgent{}
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("echo")}, func(context.Context, *tool.Invocation) (*tool.Output, error) {
		return &tool.Output{Success: true, Body: "tool result"}, nil
	})); err != nil {
		t.Fatalf("register echo: %v", err)
	}
	executedToolCalls := NewExecutedToolCallRecorder()
	loop := NewAgentLoop(&AgentLoopOptions{
		Agent:             agent,
		Dispatcher:        NewToolDispatcher(&ToolDispatcherOptions{Router: tool.NewRouter(registry), ExecutedToolCalls: executedToolCalls}),
		ExecutedToolCalls: executedToolCalls,
		MaxTurns:          3,
	})
	result, err := loop.Run(context.Background(), &AgentLoopRequest{
		Prompt:   "run echo",
		Model:    "gpt-test",
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		CWD:      "/repo",
		Tracer:   tracer,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Iterations != 2 {
		t.Fatalf("iterations = %d", result.Iterations)
	}
	if len(tracer.spans) != 2 {
		t.Fatalf("spans = %#v", tracer.spans)
	}
	for index, span := range tracer.spans {
		if span.name != SamplingRequestSpanName {
			t.Fatalf("span %d = %#v", index, span)
		}
		if span.attributes["turn_id"] != "turn-1" || span.attributes["model"] != "gpt-test" || span.attributes["cwd"] != "/repo" {
			t.Fatalf("span %d attributes = %#v", index, span.attributes)
		}
	}
}

// Rust drains the step's in-flight tool futures inside `run_sampling_request`,
// so the step's tool dispatch runs while the sampling span is still open, and
// the dispatch context carries that span (which is how the app-server's
// `mcp.tools.call` span parents to it).
func TestAgentLoopKeepsSamplingSpanOpenThroughToolDispatchLikeRust(t *testing.T) {
	tracer := &recordingSpanTracer{traceparent: "00-00000000000000000000000000000002-0000000000000003-01"}
	var (
		dispatchSpan    *recordingSpan
		dispatchEnded   bool
		dispatchCarrier string
	)
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("echo")}, func(ctx context.Context, _ *tool.Invocation) (*tool.Output, error) {
		dispatchSpan = recordingSpanFromContext(ctx)
		if dispatchSpan != nil {
			dispatchEnded = dispatchSpan.ended
		}
		if carrier, ok := protocol.TraceContextFromContext(ctx); ok {
			dispatchCarrier = carrier.Traceparent
		}
		return &tool.Output{Success: true, Body: "tool result"}, nil
	})); err != nil {
		t.Fatalf("register echo: %v", err)
	}
	executedToolCalls := NewExecutedToolCallRecorder()
	loop := NewAgentLoop(&AgentLoopOptions{
		Agent:             &fakeLoopAgent{},
		Dispatcher:        NewToolDispatcher(&ToolDispatcherOptions{Router: tool.NewRouter(registry), ExecutedToolCalls: executedToolCalls}),
		ExecutedToolCalls: executedToolCalls,
		MaxTurns:          3,
	})
	if _, err := loop.Run(context.Background(), &AgentLoopRequest{
		Prompt:   "run echo",
		Model:    "gpt-test",
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		Tracer:   tracer,
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if dispatchSpan == nil || dispatchSpan.name != SamplingRequestSpanName {
		t.Fatalf("dispatch span = %#v", dispatchSpan)
	}
	if dispatchEnded {
		t.Fatal("the sampling span was closed before the step's tool dispatch")
	}
	if dispatchCarrier != tracer.traceparent {
		t.Fatalf("dispatch carrier = %q, want %q", dispatchCarrier, tracer.traceparent)
	}
	for index, span := range tracer.spans {
		if !span.ended {
			t.Fatalf("span %d stayed open after the turn: %#v", index, span)
		}
	}
}
