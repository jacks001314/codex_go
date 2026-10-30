package tool

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
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

type environmentScopedTestExecutor struct {
	*ExecutorFunc
}

// HookEnvironmentIDs derives the target from the call arguments so the test
// can prove the router captures them before a PreToolUse input rewrite.
func (e *environmentScopedTestExecutor) HookEnvironmentIDs(invocation *Invocation) []string {
	var args struct {
		Command string `json:"command"`
	}
	if err := invocation.DecodeArguments(&args); err != nil {
		return nil
	}
	if strings.Contains(args.Command, "original") {
		return []string{"env-a"}
	}
	return []string{"env-b"}
}

type environmentRecordingHooks struct {
	pre  []string
	post []string
}

func (h *environmentRecordingHooks) RunPreToolUse(_ context.Context, _ *Invocation, payload *PreToolUsePayload) (*PreToolUseHookOutcome, error) {
	h.pre = append([]string(nil), payload.TargetEnvironments...)
	return &PreToolUseHookOutcome{UpdatedInput: map[string]any{"command": "echo rewritten"}}, nil
}

func (h *environmentRecordingHooks) RunPostToolUse(_ context.Context, _ *Invocation, payload *PostToolUsePayload) (*PostToolUseHookOutcome, error) {
	h.post = append([]string(nil), payload.TargetEnvironments...)
	return nil, nil
}

// Rust captures the invocation environments before PreToolUse and reuses them
// for PostToolUse (`ToolInvocation::environments`), so an input rewrite cannot
// retarget the environment-scoped hooks.
func TestRouterDispatchWithHooksCapturesTargetEnvironmentsLikeRust(t *testing.T) {
	registry := NewRegistry()
	executor := &environmentScopedTestExecutor{ExecutorFunc: NewExecutorFunc(Spec{Name: PlainName("Bash")}, func(ctx context.Context, invocation *Invocation) (*Output, error) {
		return &Output{Success: true, Body: "ok"}, nil
	})}
	if err := registry.Register(executor); err != nil {
		t.Fatalf("register: %v", err)
	}
	hooks := &environmentRecordingHooks{}
	router := NewRouter(registry)

	if _, err := router.DispatchWithHooks(context.Background(), &Invocation{
		CallID:   "call-1",
		ToolName: PlainName("Bash"),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{"command":"echo original"}`},
	}, hooks); err != nil {
		t.Fatalf("DispatchWithHooks() error = %v", err)
	}
	if len(hooks.pre) != 1 || hooks.pre[0] != "env-a" {
		t.Fatalf("pre targets = %#v", hooks.pre)
	}
	if len(hooks.post) != 1 || hooks.post[0] != "env-a" {
		t.Fatalf("post targets = %#v, want the pre-tool capture", hooks.post)
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

func TestRouterDispatchWithHooksStartedFiresAfterPreHooksLikeRust(t *testing.T) {
	registry := NewRegistry()
	var events []string
	if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName("Bash")}, func(ctx context.Context, invocation *Invocation) (*Output, error) {
		var args struct {
			Command string `json:"command"`
		}
		if err := invocation.DecodeArguments(&args); err != nil {
			return nil, err
		}
		events = append(events, "execute:"+args.Command)
		return &Output{Success: true, Body: "ok"}, nil
	})); err != nil {
		t.Fatalf("register: %v", err)
	}
	hooks := &fakeToolHooks{pre: &PreToolUseHookOutcome{
		UpdatedInput: map[string]any{"command": "echo rewritten"},
	}}
	router := NewRouter(registry)

	var startedCommand string
	output, err := router.DispatchWithHooksAfterPreHooks(context.Background(), &Invocation{
		CallID:   "call-1",
		ToolName: PlainName("Bash"),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{"command":"echo original"}`},
	}, hooks, func(updated *Invocation) {
		if updated == nil {
			events = append(events, "started:<nil>")
			return
		}
		var args struct {
			Command string `json:"command"`
		}
		if err := updated.DecodeArguments(&args); err == nil {
			startedCommand = args.Command
		}
		events = append(events, "started:"+startedCommand)
	})
	if err != nil {
		t.Fatalf("DispatchWithHooksAfterPreHooks() error = %v", err)
	}
	if output.Body != "ok" {
		t.Fatalf("output = %+v", output)
	}
	want := []string{"started:echo rewritten", "execute:echo rewritten"}
	if strings.Join(events, ",") != strings.Join(want, ",") {
		t.Fatalf("events = %#v, want %#v (started must run after pre-tool hooks, before execute)", events, want)
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

// TestRouterDispatchWithHooksCancelsBlockedHooksLikeRust covers Rust #51556:
// cancellation must settle dispatch even when a hook ignores its context, while
// the hook is still blocked. Their outcome settles as a cancelled tool call.
func TestRouterDispatchWithHooksCancelsBlockedHooksLikeRust(t *testing.T) {
	for _, tc := range []struct {
		name      string
		blockPre  bool
		blockPost bool
	}{
		{name: "pre hook", blockPre: true},
		{name: "post hook", blockPost: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry := NewRegistry()
			if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName("echo")}, func(ctx context.Context, invocation *Invocation) (*Output, error) {
				return &Output{Success: true, Body: "ok"}, nil
			})); err != nil {
				t.Fatalf("register: %v", err)
			}
			hooks := &blockingHookRunner{
				blockPre:  tc.blockPre,
				blockPost: tc.blockPost,
				started:   make(chan struct{}),
				release:   make(chan struct{}),
			}
			router := NewRouter(registry)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go func() {
				<-hooks.started
				cancel()
			}()

			done := make(chan error, 1)
			go func() {
				_, err := router.DispatchWithHooks(ctx, &Invocation{
					CallID:   "call-1",
					ToolName: PlainName("echo"),
					Payload:  Payload{Kind: PayloadFunction, Arguments: `{}`},
				}, hooks)
				done <- err
			}()
			select {
			case err := <-done:
				if !errors.Is(err, ErrToolCancelled) {
					t.Fatalf("dispatch error = %v, want ErrToolCancelled", err)
				}
			case <-time.After(time.Second):
				t.Fatal("dispatch did not settle while the hook remained blocked")
			}
			close(hooks.release)
		})
	}
}

// TestRouterWaitUntilReadyCancelsBlockedWaiterLikeRust covers Rust #51556's
// cancellable readiness wait: an interrupted call must not wait for a readiness
// source that ignores its context.
func TestRouterWaitUntilReadyCancelsBlockedWaiterLikeRust(t *testing.T) {
	registry := NewRegistry()
	waiter := &blockingReadinessExecutor{
		ExecutorFunc: NewExecutorFunc(Spec{Name: PlainName("waiting")}, func(context.Context, *Invocation) (*Output, error) {
			return &Output{Success: true}, nil
		}),
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	if err := registry.Register(waiter); err != nil {
		t.Fatalf("register: %v", err)
	}
	router := NewRouter(registry)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-waiter.started
		cancel()
	}()

	done := make(chan error, 1)
	go func() {
		done <- router.WaitUntilReady(ctx, &Invocation{CallID: "call-1", ToolName: PlainName("waiting")})
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrToolCancelled) {
			t.Fatalf("WaitUntilReady() error = %v, want ErrToolCancelled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("WaitUntilReady did not settle while the waiter remained blocked")
	}
	close(waiter.release)
}

type blockingHookRunner struct {
	blockPre  bool
	blockPost bool
	started   chan struct{}
	release   chan struct{}
}

func (h *blockingHookRunner) block() {
	close(h.started)
	<-h.release
}

func (h *blockingHookRunner) RunPreToolUse(context.Context, *Invocation, *PreToolUsePayload) (*PreToolUseHookOutcome, error) {
	if h.blockPre {
		h.block()
	}
	return nil, nil
}

func (h *blockingHookRunner) RunPostToolUse(context.Context, *Invocation, *PostToolUsePayload) (*PostToolUseHookOutcome, error) {
	if h.blockPost {
		h.block()
	}
	return nil, nil
}

type blockingReadinessExecutor struct {
	*ExecutorFunc
	started chan struct{}
	release chan struct{}
}

func (e *blockingReadinessExecutor) WaitUntilReady(context.Context, *Invocation) error {
	close(e.started)
	<-e.release
	return nil
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
