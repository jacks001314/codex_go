package turn

import (
	"context"
	"errors"
	"testing"

	"codex_go/tool"
)

// blockingDynamicToolCaller waits until the request context is cancelled and
// then reports the context error, mirroring a dynamic-tool client that never
// answers (Rust #51556 dynamic_tool_cancellation suite).
type blockingDynamicToolCaller struct {
	invoked chan struct{}
}

func (c *blockingDynamicToolCaller) Request(ctx context.Context, method string, params any, target any) error {
	if c.invoked != nil {
		select {
		case c.invoked <- struct{}{}:
		default:
		}
	}
	<-ctx.Done()
	return ctx.Err()
}

func dynamicToolCancellationRegistry(t *testing.T, caller DynamicToolCaller) *tool.Registry {
	t.Helper()
	options := DefaultToolRegistryOptions(t.TempDir())
	options.EnableCore = false
	options.EnableShell = false
	options.EnableApplyPatch = false
	options.EnableMCP = false
	options.EnableAgents = false
	options.DynamicToolCaller = caller
	options.DynamicTools = []DynamicToolSpec{{Type: "function", Function: &DynamicToolFunctionSpec{
		Name: "gate", InputSchema: map[string]any{"type": "object"},
	}}}
	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry() error = %v", err)
	}
	return registry
}

func dynamicToolCancellationInvocation() *tool.Invocation {
	return &tool.Invocation{
		CallID: "call-1", ToolName: tool.PlainName("gate"),
		Payload: tool.Payload{Kind: tool.PayloadFunction, Arguments: `{}`},
	}
}

// TestDynamicToolCancellationSettlesWithFailedItemLikeRust mirrors Rust
// dynamic_tests.rs::response_cancel_race_completes_once and the cancellation
// half of the dynamic_tool_cancellation suite: a call cancelled while awaiting a
// response completes with a failed item and the cancellation error instead of
// being dropped.
func TestDynamicToolCancellationSettlesWithFailedItemLikeRust(t *testing.T) {
	caller := &blockingDynamicToolCaller{invoked: make(chan struct{}, 1)}
	registry := dynamicToolCancellationRegistry(t, caller)
	executor, ok := registry.Lookup(tool.PlainName("gate"))
	if !ok {
		t.Fatal("dynamic tool missing")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		select {
		case <-caller.invoked:
		case <-done:
		}
		cancel()
	}()
	output, err := executor.Execute(ctx, dynamicToolCancellationInvocation())
	close(done)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output.Success {
		t.Fatalf("cancelled dynamic tool reported success: %#v", output)
	}
	if output.Error != dynamicToolCancelledMessage || output.Body != dynamicToolCancelledMessage {
		t.Fatalf("output = %#v, want cancellation message %q", output, dynamicToolCancelledMessage)
	}
}

// TestDynamicToolCancellationBeforeRequestLikeRust mirrors the
// "cancellation before dispatch admission" case: when the invocation is already
// cancelled the handler settles with the cancellation item and never reaches the
// request sink.
func TestDynamicToolCancellationBeforeRequestLikeRust(t *testing.T) {
	caller := &fakeDynamicToolCaller{result: &DynamicToolCallResponse{Success: true}}
	registry := dynamicToolCancellationRegistry(t, caller)
	executor, ok := registry.Lookup(tool.PlainName("gate"))
	if !ok {
		t.Fatal("dynamic tool missing")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	output, err := executor.Execute(ctx, dynamicToolCancellationInvocation())
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output.Success || output.Error != dynamicToolCancelledMessage {
		t.Fatalf("output = %#v, want cancellation message", output)
	}
	if caller.method != "" {
		t.Fatalf("a cancelled call reached the request sink: method = %q", caller.method)
	}
}

// TestDynamicToolFinishesOnCancellationSpecLikeRust pins Rust #51556's
// `DynamicToolHandler::finishes_on_cancellation() == true` override: dynamic
// tools declare that they observe cancellation, while built-in tools keep the
// trait default.
func TestDynamicToolFinishesOnCancellationSpecLikeRust(t *testing.T) {
	caller := &fakeDynamicToolCaller{result: &DynamicToolCallResponse{Success: true}}
	registry := dynamicToolCancellationRegistry(t, caller)
	router := tool.NewRouter(registry)
	if !router.FinishesOnCancellation(tool.PlainName("gate")) {
		t.Fatal("dynamic tool did not declare finishes_on_cancellation")
	}
	builtin := DefaultToolRegistryOptions(t.TempDir())
	builtin.EnableMCP = false
	builtinRegistry, err := BuildToolRegistry(builtin)
	if err != nil {
		t.Fatalf("BuildToolRegistry() error = %v", err)
	}
	if tool.NewRouter(builtinRegistry).FinishesOnCancellation(tool.PlainName("update_plan")) {
		t.Fatal("a built-in tool declared finishes_on_cancellation")
	}
}

// blockingCancellationExecutor blocks until its invocation is cancelled and then
// returns the context error, like a hook or handler that observes cancellation.
type blockingCancellationExecutor struct {
	spec    tool.Spec
	invoked chan struct{}
}

func (e *blockingCancellationExecutor) Spec() tool.Spec { return e.spec }

func (e *blockingCancellationExecutor) Execute(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
	if e.invoked != nil {
		select {
		case e.invoked <- struct{}{}:
		default:
		}
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func dispatcherCancellationRegistry(t *testing.T, finishesOnCancellation bool) (*tool.Registry, chan struct{}) {
	t.Helper()
	invoked := make(chan struct{}, 1)
	registry := tool.NewRegistry()
	executor := &blockingCancellationExecutor{
		spec: tool.Spec{
			Name:                   tool.PlainName("test_tool"),
			FinishesOnCancellation: finishesOnCancellation,
		},
		invoked: invoked,
	}
	if err := registry.Register(executor); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	return registry, invoked
}

// TestToolDispatcherCancellationReportsAbortedOutcomeLikeRust mirrors Rust
// #51556's registry change: when cancellation interrupts dispatch for a tool
// that finishes on cancellation, the call is reported as an aborted lifecycle
// outcome (a terminal failed item) instead of a fatal error that drops the call.
func TestToolDispatcherCancellationReportsAbortedOutcomeLikeRust(t *testing.T) {
	registry, invoked := dispatcherCancellationRegistry(t, true)
	dispatcher := NewToolDispatcher(&ToolDispatcherOptions{Router: tool.NewRouter(registry)})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-invoked
		cancel()
	}()
	result, err := dispatcher.executeToolInvocation(ctx, &tool.Invocation{
		CallID: "abort-1", ToolName: tool.PlainName("test_tool"),
		Payload: tool.Payload{Kind: tool.PayloadFunction, Arguments: `{}`},
	})
	if err != nil {
		t.Fatalf("executeToolInvocation() error = %v", err)
	}
	if result == nil || result.Output == nil {
		t.Fatalf("result = %#v", result)
	}
	if !result.Aborted {
		t.Fatalf("outcome = %#v, want Aborted", result)
	}
	if result.Output.Success || result.Output.Error != "tool call cancelled" {
		t.Fatalf("output = %#v, want the respond-to-model cancellation", result.Output)
	}
}

// TestToolDispatcherCancellationWithoutObserverStaysFatalLikeRust pins the
// trait default: a tool that does not observe cancellation keeps the fatal
// dispatch error.
func TestToolDispatcherCancellationWithoutObserverStaysFatalLikeRust(t *testing.T) {
	registry, invoked := dispatcherCancellationRegistry(t, false)
	dispatcher := NewToolDispatcher(&ToolDispatcherOptions{Router: tool.NewRouter(registry)})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-invoked
		cancel()
	}()
	_, err := dispatcher.executeToolInvocation(ctx, &tool.Invocation{
		CallID: "abort-2", ToolName: tool.PlainName("test_tool"),
		Payload: tool.Payload{Kind: tool.PayloadFunction, Arguments: `{}`},
	})
	if err == nil {
		t.Fatal("a non-observing tool did not report the cancellation as a fatal error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want the context cancellation", err)
	}
}
