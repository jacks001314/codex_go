package tool

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

// Mirrors Rust #44866: a yielded Code Mode cell can outlive its turn, but a
// nested call whose originating step context was released is cancelled instead
// of being dispatched against whatever execution happens to be active.
func TestCodeModeNestedCallCancelledAfterCellClosedLikeRust(t *testing.T) {
	runtime := NewCodeModeRuntime(nil, false)
	defer func() { _ = runtime.Close() }()
	registry := NewRegistry()
	var mu sync.Mutex
	calls := 0
	if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName("nested-tool")}, func(_ context.Context, _ *Invocation) (*Output, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return &Output{Success: true, Body: "ok"}, nil
	})); err != nil {
		t.Fatalf("register nested tool: %v", err)
	}
	_, _ = runtime.Executors(registry)
	delegate := &codeModeRemoteDelegate{exec: runtime.exec}
	release := delegate.begin(&Invocation{CallID: "call-exec"})
	defer release()

	call := CodeModeRemoteNestedCall{
		CellID: "closed-cell", RuntimeToolCallID: "call-exec-nested",
		ToolName: PlainName("nested-tool"), Kind: PayloadFunction, Input: json.RawMessage(`{}`),
	}
	if _, err := delegate.Invoke(context.Background(), call); err != nil {
		t.Fatalf("nested call while the cell is live: %v", err)
	}

	delegate.CellClosed("closed-cell")
	_, err := delegate.Invoke(context.Background(), CodeModeRemoteNestedCall{
		CellID: "closed-cell", RuntimeToolCallID: "call-exec-nested-2",
		ToolName: PlainName("nested-tool"), Kind: PayloadFunction, Input: json.RawMessage(`{}`),
	})
	if err == nil || !strings.Contains(err.Error(), codeModeNestedToolCallCancelled) {
		t.Fatalf("late nested call error = %v, want %q", err, codeModeNestedToolCallCancelled)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("nested executor ran %d times, want 1 (the cancelled call must not dispatch)", calls)
	}
}

// Mirrors Rust #44866's Weak<StepContext> upgrade: once the execution that owns
// the cell is released, a queued nested call fails with the delegate's
// cancellation error instead of running with an empty context.
func TestCodeModeNestedCallCancelledWhenOriginatingExecutionReleasedLikeRust(t *testing.T) {
	runtime := NewCodeModeRuntime(nil, false)
	defer func() { _ = runtime.Close() }()
	registry := NewRegistry()
	ran := false
	if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName("nested-tool")}, func(_ context.Context, _ *Invocation) (*Output, error) {
		ran = true
		return &Output{Success: true, Body: "ok"}, nil
	})); err != nil {
		t.Fatalf("register nested tool: %v", err)
	}
	_, _ = runtime.Executors(registry)
	delegate := &codeModeRemoteDelegate{exec: runtime.exec}
	release := delegate.begin(&Invocation{CallID: "call-exec"})
	if _, err := delegate.Invoke(context.Background(), CodeModeRemoteNestedCall{
		CellID: "live-cell", RuntimeToolCallID: "call-exec-nested",
		ToolName: PlainName("nested-tool"), Kind: PayloadFunction, Input: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("nested call while the context is live: %v", err)
	}
	if !ran {
		t.Fatal("nested executor did not run while the context was live")
	}
	ran = false
	release()
	if _, err := delegate.Invoke(context.Background(), CodeModeRemoteNestedCall{
		CellID: "live-cell", RuntimeToolCallID: "call-exec-nested-2",
		ToolName: PlainName("nested-tool"), Kind: PayloadFunction, Input: json.RawMessage(`{}`),
	}); err == nil || !strings.Contains(err.Error(), codeModeNestedToolCallCancelled) {
		t.Fatalf("nested call after release error = %v, want %q", err, codeModeNestedToolCallCancelled)
	}
	if ran {
		t.Fatal("a nested call with a released context must not dispatch")
	}
}

// A cell id that starts again clears its closed marker, so the runtime's cell
// reuse does not cancel legitimate work.
func TestCodeModeClosedCellMarkerClearsOnRestartLikeRust(t *testing.T) {
	runtime := NewCodeModeRuntime(nil, false)
	defer func() { _ = runtime.Close() }()
	registry := NewRegistry()
	if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName("nested-tool")}, func(_ context.Context, _ *Invocation) (*Output, error) {
		return &Output{Success: true, Body: "ok"}, nil
	})); err != nil {
		t.Fatalf("register nested tool: %v", err)
	}
	_, _ = runtime.Executors(registry)
	delegate := &codeModeRemoteDelegate{exec: runtime.exec}
	release := delegate.begin(&Invocation{CallID: "call-exec"})
	defer release()

	delegate.CellClosed("reused-cell")
	if !runtime.exec.remoteCellClosed("reused-cell") {
		t.Fatal("cell close must be remembered")
	}
	runtime.exec.clearRemoteCellClosed("reused-cell")
	if runtime.exec.remoteCellClosed("reused-cell") {
		t.Fatal("a restarted cell must not stay cancelled")
	}
	if _, err := delegate.Invoke(context.Background(), CodeModeRemoteNestedCall{
		CellID: "reused-cell", RuntimeToolCallID: "call-exec-nested",
		ToolName: PlainName("nested-tool"), Kind: PayloadFunction, Input: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("nested call after restart: %v", err)
	}
}
