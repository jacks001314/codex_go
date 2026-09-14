package turn

import (
	"context"
	"testing"

	"codex_go/model"
	"codex_go/tool"
)

// recordingSpanTracer captures the spans the turn loop opens.
type recordingSpanTracer struct {
	spans []recordingSpan
}

type recordingSpan struct {
	name       string
	attributes map[string]string
	parent     *recordingSpan
	context    context.Context
}

func (t *recordingSpanTracer) StartSpan(ctx context.Context, parent model.TelemetrySpan, name string, attributes map[string]string) (context.Context, model.TelemetrySpan) {
	span := &recordingSpan{name: name, attributes: attributes, context: ctx}
	if concrete, ok := parent.(*recordingSpan); ok {
		span.parent = concrete
	}
	t.spans = append(t.spans, *span)
	return context.Background(), span
}

func (s *recordingSpan) End()                     {}
func (s *recordingSpan) Record(map[string]string) {}
func (s *recordingSpan) SetName(string)           {}

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
