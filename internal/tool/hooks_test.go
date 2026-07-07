package tool

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestRouterDispatchWithHooksBlocksBeforeTool(t *testing.T) {
	registry := NewRegistry()
	called := false
	if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName("Bash")}, func(ctx context.Context, invocation *Invocation) (*Output, error) {
		called = true
		return &Output{Success: true, Body: "ran"}, nil
	})); err != nil {
		t.Fatalf("register: %v", err)
	}
	hooks := &fakeToolHooks{pre: &PreToolUseHookOutcome{Blocked: true, BlockReason: "nope"}}
	router := NewRouter(registry)

	_, err := router.DispatchWithHooks(context.Background(), &Invocation{
		CallID:   "call-1",
		ToolName: PlainName("Bash"),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{"command":"rm -rf /tmp/x"}`},
	}, hooks)

	if err == nil || !strings.Contains(err.Error(), "Command blocked by PreToolUse hook: nope") {
		t.Fatalf("error = %v", err)
	}
	if called {
		t.Fatal("tool executed after pre hook block")
	}
}

func TestRouterDispatchWithHooksRewritesInput(t *testing.T) {
	registry := NewRegistry()
	var command string
	var contexts []string
	if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName("Bash")}, func(ctx context.Context, invocation *Invocation) (*Output, error) {
		var args struct {
			Command string `json:"command"`
		}
		if err := invocation.DecodeArguments(&args); err != nil {
			return nil, err
		}
		command = args.Command
		if value, ok := invocation.Context["additional_contexts"].([]string); ok {
			contexts = append([]string(nil), value...)
		}
		return &Output{Success: true, Body: "ok"}, nil
	})); err != nil {
		t.Fatalf("register: %v", err)
	}
	hooks := &fakeToolHooks{pre: &PreToolUseHookOutcome{
		UpdatedInput:       map[string]any{"command": "echo rewritten"},
		AdditionalContexts: []string{"pre context"},
	}}
	router := NewRouter(registry)

	output, err := router.DispatchWithHooks(context.Background(), &Invocation{
		CallID:   "call-1",
		ToolName: PlainName("Bash"),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{"command":"echo original"}`},
	}, hooks)

	if err != nil {
		t.Fatalf("DispatchWithHooks() error = %v", err)
	}
	if output.Body != "ok" || command != "echo rewritten" {
		t.Fatalf("output = %+v command = %q", output, command)
	}
	if len(contexts) != 1 || contexts[0] != "pre context" {
		t.Fatalf("contexts = %#v", contexts)
	}
}

func TestRouterDispatchWithHooksPostFeedbackReplacesBody(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName("echo")}, func(ctx context.Context, invocation *Invocation) (*Output, error) {
		return &Output{Success: true, Body: "original"}, nil
	})); err != nil {
		t.Fatalf("register: %v", err)
	}
	hooks := &fakeToolHooks{post: &PostToolUseHookOutcome{FeedbackMessage: "hook feedback"}}
	router := NewRouter(registry)

	output, err := router.DispatchWithHooks(context.Background(), &Invocation{
		CallID:   "call-1",
		ToolName: PlainName("echo"),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{}`},
	}, hooks)

	if err != nil {
		t.Fatalf("DispatchWithHooks() error = %v", err)
	}
	if output.Body != "hook feedback" {
		t.Fatalf("output = %+v", output)
	}
}

func TestRouterDispatchWithHooksAppendsPostAdditionalContext(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName("echo")}, func(ctx context.Context, invocation *Invocation) (*Output, error) {
		return &Output{Success: true, Body: "original", Data: map[string]any{"hook_response": "original"}}, nil
	})); err != nil {
		t.Fatalf("register: %v", err)
	}
	hooks := &fakeToolHooks{post: &PostToolUseHookOutcome{AdditionalContexts: []string{"ctx one", "ctx two"}}}
	router := NewRouter(registry)

	output, err := router.DispatchWithHooks(context.Background(), &Invocation{
		CallID:   "call-1",
		ToolName: PlainName("echo"),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{}`},
	}, hooks)

	if err != nil {
		t.Fatalf("DispatchWithHooks() error = %v", err)
	}
	contexts, ok := output.Data["additional_contexts"].([]string)
	if !ok || len(contexts) != 2 || contexts[0] != "ctx one" {
		t.Fatalf("contexts = %#v", output.Data["additional_contexts"])
	}
	if !strings.Contains(output.Body, "Additional context from PostToolUse hook") {
		t.Fatalf("Body = %q", output.Body)
	}
}

func TestRouterDispatchWithHooksPostBlockReturnsModelError(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName("echo")}, func(ctx context.Context, invocation *Invocation) (*Output, error) {
		return &Output{Success: true, Body: "original"}, nil
	})); err != nil {
		t.Fatalf("register: %v", err)
	}
	hooks := &fakeToolHooks{post: &PostToolUseHookOutcome{Blocked: true, FeedbackMessage: "review output"}}
	router := NewRouter(registry)

	_, err := router.DispatchWithHooks(context.Background(), &Invocation{
		CallID:   "call-1",
		ToolName: PlainName("echo"),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{}`},
	}, hooks)

	if err == nil || !strings.Contains(err.Error(), "review output") {
		t.Fatalf("error = %v", err)
	}
	var callErr *FunctionCallError
	if !AsFunctionCallError(err, &callErr) || !callErr.RespondsToModel() {
		t.Fatalf("expected model-visible error, got %T %v", err, err)
	}
}

func TestRouterDispatchWithHooksFatalStopsTool(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName("echo")}, func(ctx context.Context, invocation *Invocation) (*Output, error) {
		return &Output{Success: true, Body: "original"}, nil
	})); err != nil {
		t.Fatalf("register: %v", err)
	}
	hooks := &fakeToolHooks{pre: &PreToolUseHookOutcome{Fatal: true, FatalReason: "stop now"}}
	router := NewRouter(registry)

	_, err := router.DispatchWithHooks(context.Background(), &Invocation{
		CallID:   "call-1",
		ToolName: PlainName("echo"),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{}`},
	}, hooks)

	if err == nil || !strings.Contains(err.Error(), "Fatal error") {
		t.Fatalf("error = %v", err)
	}
	var callErr *FunctionCallError
	if !AsFunctionCallError(err, &callErr) || !callErr.IsFatal() {
		t.Fatalf("expected fatal error, got %T %v", err, err)
	}
}

func TestRouterDispatchWithHooksPreservesHookRespondToModelError(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName("echo")}, func(ctx context.Context, invocation *Invocation) (*Output, error) {
		return &Output{Success: true, Body: "original"}, nil
	})); err != nil {
		t.Fatalf("register: %v", err)
	}
	hooks := &fakeToolHooks{preErr: RespondToModel("hook says no")}
	router := NewRouter(registry)

	_, err := router.DispatchWithHooks(context.Background(), &Invocation{
		CallID:   "call-1",
		ToolName: PlainName("echo"),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{}`},
	}, hooks)

	var callErr *FunctionCallError
	if !AsFunctionCallError(err, &callErr) || !callErr.RespondsToModel() || callErr.ModelMessage() != "hook says no" {
		t.Fatalf("error = %T %v", err, err)
	}
}

func TestRouterDispatchWithHooksPlainHookErrorIsFatal(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName("echo")}, func(ctx context.Context, invocation *Invocation) (*Output, error) {
		return &Output{Success: true, Body: "original"}, nil
	})); err != nil {
		t.Fatalf("register: %v", err)
	}
	hooks := &fakeToolHooks{preErr: fmt.Errorf("disk failed")}
	router := NewRouter(registry)

	_, err := router.DispatchWithHooks(context.Background(), &Invocation{
		CallID:   "call-1",
		ToolName: PlainName("echo"),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{}`},
	}, hooks)

	var callErr *FunctionCallError
	if !AsFunctionCallError(err, &callErr) || !callErr.IsFatal() {
		t.Fatalf("error = %T %v", err, err)
	}
}

type fakeToolHooks struct {
	pre     *PreToolUseHookOutcome
	post    *PostToolUseHookOutcome
	preErr  error
	postErr error
}

func (h *fakeToolHooks) RunPreToolUse(ctx context.Context, invocation *Invocation, payload *PreToolUsePayload) (*PreToolUseHookOutcome, error) {
	_ = ctx
	_ = invocation
	_ = payload
	if h.preErr != nil {
		return nil, h.preErr
	}
	return h.pre, nil
}

func (h *fakeToolHooks) RunPostToolUse(ctx context.Context, invocation *Invocation, payload *PostToolUsePayload) (*PostToolUseHookOutcome, error) {
	_ = ctx
	_ = invocation
	_ = payload
	if h.postErr != nil {
		return nil, h.postErr
	}
	return h.post, nil
}
