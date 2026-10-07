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
	// order is a monotonic counter shared by span creation and span end, so a
	// test can order phase boundaries (Rust's span start/end timestamps).
	order int
}

type recordingSpan struct {
	name        string
	attributes  map[string]string
	parent      *recordingSpan
	context     context.Context
	traceparent string
	tracestate  string
	ended       bool
	tracer      *recordingSpanTracer
	startOrder  int
	endOrder    int
}

// recordingSpanKey carries a recorded span in its context, so a test can prove
// which span a callback ran under.
type recordingSpanKey struct{}

func (t *recordingSpanTracer) StartSpan(ctx context.Context, parent model.TelemetrySpan, name string, attributes map[string]string) (context.Context, model.TelemetrySpan) {
	span := &recordingSpan{name: name, attributes: attributes, context: ctx, tracer: t}
	span.traceparent = t.traceparent
	span.startOrder = t.order
	t.order++
	if concrete, ok := parent.(*recordingSpan); ok {
		span.parent = concrete
	}
	t.spans = append(t.spans, span)
	return context.WithValue(ctx, recordingSpanKey{}, span), span
}

// recordedSpansByName returns the recorded spans with the given name, in
// creation order.
func recordedSpansByName(tracer *recordingSpanTracer, name string) []*recordingSpan {
	var out []*recordingSpan
	for _, span := range tracer.spans {
		if span.name == name {
			out = append(out, span)
		}
	}
	return out
}

// recordingSpanFromContext returns the span the recorder stored in ctx, or nil.
func recordingSpanFromContext(ctx context.Context) *recordingSpan {
	if ctx == nil {
		return nil
	}
	span, _ := ctx.Value(recordingSpanKey{}).(*recordingSpan)
	return span
}

func (s *recordingSpan) End() {
	if s.ended {
		return
	}
	s.ended = true
	if s.tracer != nil {
		s.endOrder = s.tracer.order
		s.tracer.order++
	}
}
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
	samplingSpans := recordedSpansByName(tracer, SamplingRequestSpanName)
	if len(samplingSpans) != 2 {
		t.Fatalf("sampling-request spans = %#v", tracer.spans)
	}
	for index, span := range samplingSpans {
		if span.attributes["turn_id"] != "turn-1" || span.attributes["model"] != "gpt-test" || span.attributes["cwd"] != "/repo" {
			t.Fatalf("span %d attributes = %#v", index, span.attributes)
		}
	}
}

// TestAgentLoopSamplingSpanCarriesUsageTagsLikeRust mirrors Rust #46501: the
// sampling-request span records the turn's usage-tag document as `tags_json`
// (sorted keys), and omits the field when the turn has no tags.
func TestAgentLoopSamplingSpanCarriesUsageTagsLikeRust(t *testing.T) {
	tracer := &recordingSpanTracer{traceparent: "00-00000000000000000000000000000004-0000000000000005-01"}
	loop := NewAgentLoop(&AgentLoopOptions{
		Agent:             &fakeLoopAgent{},
		Dispatcher:        NewToolDispatcher(&ToolDispatcherOptions{Router: tool.NewRouter(tool.NewRegistry())}),
		ExecutedToolCalls: NewExecutedToolCallRecorder(),
		MaxTurns:          2,
	})
	if _, err := loop.Run(context.Background(), &AgentLoopRequest{
		Prompt:    "hello",
		Model:     "gpt-test",
		ThreadID:  "thread-tags",
		TurnID:    "turn-tags",
		CWD:       "/repo",
		Tracer:    tracer,
		UsageTags: map[string]string{"service_tier": "priority", "model_context_window": "unset"},
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(tracer.spans) == 0 {
		t.Fatal("no sampling span was recorded")
	}
	want := `{"model_context_window":"unset","service_tier":"priority"}`
	if got := tracer.spans[0].attributes["tags_json"]; got != want {
		t.Fatalf("tags_json = %q, want %q", got, want)
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

// Rust #49262: the turn loop brackets sampling and the tool drain with phase
// spans `codex.sampling` and `codex.tool_blocking`, each carrying
// `codex.turn.phase` plus the conversation and turn ids, both nested under the
// sampling-request span. Sampling ends before the tools drain.
func TestAgentLoopOpensTurnPhaseSpansLikeRust(t *testing.T) {
	tracer := &recordingSpanTracer{}
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("echo")}, func(context.Context, *tool.Invocation) (*tool.Output, error) {
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

	samplingRequest := recordedSpansByName(tracer, SamplingRequestSpanName)
	samplingPhase := recordedSpansByName(tracer, SamplingPhaseSpanName)
	toolBlocking := recordedSpansByName(tracer, ToolBlockingPhaseSpanName)
	if len(samplingRequest) != 2 || len(samplingPhase) != 2 || len(toolBlocking) != 1 {
		t.Fatalf("spans = %#v", tracer.spans)
	}
	for index, span := range samplingPhase {
		if attributes := span.attributes; attributes[TurnPhaseAttribute] != "sampling" ||
			attributes[ConversationIDAttribute] != "thread-1" || attributes[TurnIDAttribute] != "turn-1" {
			t.Fatalf("sampling phase span %d attributes = %#v", index, attributes)
		}
		if span.parent != samplingRequest[index] {
			t.Fatalf("sampling phase span %d parent = %#v", index, span.parent)
		}
	}
	if attributes := toolBlocking[0].attributes; attributes[TurnPhaseAttribute] != "tool_blocking" ||
		attributes[ConversationIDAttribute] != "thread-1" || attributes[TurnIDAttribute] != "turn-1" {
		t.Fatalf("tool-blocking phase span attributes = %#v", attributes)
	}
	if toolBlocking[0].parent != samplingRequest[0] {
		t.Fatalf("tool-blocking phase span parent = %#v", toolBlocking[0].parent)
	}
	// Sampling must end before the tools drain, like Rust dropping the sampling
	// span before `drain_in_flight`.
	if !(samplingPhase[0].endOrder < toolBlocking[0].startOrder) {
		t.Fatalf("sampling ended at %d, tool blocking started at %d", samplingPhase[0].endOrder, toolBlocking[0].startOrder)
	}
	for index, span := range tracer.spans {
		if !span.ended {
			t.Fatalf("span %d stayed open after the turn: %#v", index, span)
		}
	}
}

// Rust #49262 also brackets automatic compaction with a `codex.compaction` span
// carrying `codex.turn.phase = "compaction"`.
func TestAgentLoopOpensCompactionPhaseSpanLikeRust(t *testing.T) {
	tracer := &recordingSpanTracer{}
	loop := NewAgentLoop(&AgentLoopOptions{Agent: &rollOverLoopAgent{}, MaxTurns: 3})
	replacement := []any{map[string]any{"type": "message", "role": "user"}}
	compactionSpanAtCall := (*recordingSpan)(nil)
	if _, err := loop.Run(context.Background(), &AgentLoopRequest{
		Prompt:   "run",
		Model:    "gpt-test",
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		Tracer:   tracer,
		SamplingCompaction: func(*SamplingCompactionContext) (*SamplingCompactionResult, error) {
			compactionSpans := recordedSpansByName(tracer, CompactionPhaseSpanName)
			if len(compactionSpans) > 0 {
				compactionSpanAtCall = compactionSpans[len(compactionSpans)-1]
			}
			return &SamplingCompactionResult{Compacted: true, InputItems: replacement}, nil
		},
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	compactionSpans := recordedSpansByName(tracer, CompactionPhaseSpanName)
	if len(compactionSpans) != 1 {
		t.Fatalf("compaction phase spans = %#v", tracer.spans)
	}
	span := compactionSpans[0]
	if attributes := span.attributes; attributes[TurnPhaseAttribute] != "compaction" ||
		attributes[ConversationIDAttribute] != "thread-1" || attributes[TurnIDAttribute] != "turn-1" {
		t.Fatalf("compaction phase span attributes = %#v", attributes)
	}
	if compactionSpanAtCall != span {
		t.Fatal("the compaction callback did not run inside the compaction phase span")
	}
	if !span.ended {
		t.Fatalf("compaction phase span stayed open: %#v", span)
	}
}
