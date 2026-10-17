package execserver

import (
	"context"
	"sync"
	"testing"
	"time"

	"codex_go/tracehook"
)

func TestExecServerRPCAndProcessTimingEventsLikeRust(t *testing.T) {
	root := newRecordingTraceSpan()
	ctx := tracehook.WithSpan(context.Background(), root)
	server := NewServer()
	server.handleLineWithLabel(ctx, []byte(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"test","version":"1"}}}`,
	), "trace test")

	processCtx, processSpan := startExecServerSpan(
		ctx,
		execServerProcessStartSpan,
		execServerProcessSpanAttributes("process-123", nil),
	)
	if processCtx == nil || processSpan == nil {
		t.Fatal("process span was not created")
	}
	processSpan.End()

	attributes := root.attributesSnapshot()
	if attributes["rpc.dispatch_offset_ns"] == "" ||
		attributes["rpc.response_enqueue_offset_ns"] == "" {
		t.Fatalf("request span attributes = %#v", attributes)
	}
	if !root.hasEvent(execServerResponseEnqueuedEvent) {
		t.Fatalf("request span events = %#v, want %s", root.eventNames(), execServerResponseEnqueuedEvent)
	}
	RpcCompletion{completedAt: time.Now()}.recordReceipt(ctx, MethodInitialize)
	if !root.hasEvent(execServerResponseReceivedEvent) {
		t.Fatalf("request span events = %#v, want %s", root.eventNames(), execServerResponseReceivedEvent)
	}
	children := root.childrenSnapshot()
	if len(children) != 1 || children[0].name != execServerProcessStartSpan ||
		children[0].attributes["process.id"] != "process-123" || !children[0].ended {
		t.Fatalf("process span = %#v", children)
	}
}

func TestTraceProcessIDBoundsLikeRust(t *testing.T) {
	if got := traceProcessID(""); got != "" {
		t.Fatalf("empty process id = %q", got)
	}
	if got := traceProcessID("process-1"); got != "process-1" {
		t.Fatalf("process id = %q", got)
	}
	if got := traceProcessID(string(make([]byte, 65))); got != "" {
		t.Fatalf("oversized process id = %q", got)
	}
}

type recordingTraceSpan struct {
	mu         sync.Mutex
	name       string
	attributes map[string]string
	events     map[string]map[string]string
	children   []*recordingTraceSpan
	ended      bool
}

func newRecordingTraceSpan() *recordingTraceSpan {
	return &recordingTraceSpan{
		attributes: map[string]string{},
		events:     map[string]map[string]string{},
	}
}

func (s *recordingTraceSpan) AddEvent(name string, attributes map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	clone := make(map[string]string, len(attributes))
	for key, value := range attributes {
		clone[key] = value
	}
	s.events[name] = clone
}

func (s *recordingTraceSpan) SetAttribute(key string, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attributes[key] = value
}

func (s *recordingTraceSpan) End() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ended = true
}

func (s *recordingTraceSpan) StartChildSpan(name string, attributes map[string]string) tracehook.Span {
	child := newRecordingTraceSpan()
	child.name = name
	for key, value := range attributes {
		child.attributes[key] = value
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.children = append(s.children, child)
	return child
}

func (s *recordingTraceSpan) attributesSnapshot() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.attributes))
	for key, value := range s.attributes {
		out[key] = value
	}
	return out
}

func (s *recordingTraceSpan) childrenSnapshot() []*recordingTraceSpan {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*recordingTraceSpan(nil), s.children...)
}

func (s *recordingTraceSpan) hasEvent(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.events[name]
	return ok
}

func (s *recordingTraceSpan) eventNames() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := make([]string, 0, len(s.events))
	for name := range s.events {
		names = append(names, name)
	}
	return names
}
