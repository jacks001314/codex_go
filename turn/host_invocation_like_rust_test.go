package turn

import (
	"context"
	"errors"
	"strings"
	"testing"

	"codex_go/agent"
	"codex_go/model"
	"codex_go/tool"
)

// Mirrors Rust #57586: dispatch reserves the call ID, publishes the live
// invocation only after pre-tool hooks admit it, rejects a duplicate active ID
// in the same turn as a model-facing error, and revokes lookup when dispatch
// ends.
func TestToolDispatcherReservesLiveCallIDsLikeRust(t *testing.T) {
	registry := agent.NewInvocationRegistry()
	entered := make(chan struct{})
	release := make(chan struct{})
	liveCall := make(chan *agent.ToolInvocationContext, 1)
	handlerFailure := make(chan error, 1)
	toolRegistry := tool.NewRegistry()
	if err := toolRegistry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("probe")}, func(_ context.Context, invocation *tool.Invocation) (*tool.Output, error) {
		live, err := registry.LookupToolInvocation("thread-1", "turn-1", invocation.CallID)
		if err != nil {
			handlerFailure <- err
			close(entered)
			<-release
			return nil, err
		}
		liveCall <- live
		close(entered)
		<-release
		return &tool.Output{CallID: invocation.CallID, ToolName: invocation.ToolName, Success: true, Body: "ok"}, nil
	})); err != nil {
		t.Fatalf("register executor: %v", err)
	}
	dispatcher := NewToolDispatcher(&ToolDispatcherOptions{
		Router:             tool.NewRouter(toolRegistry),
		ThreadID:           "thread-1",
		TurnID:             "turn-1",
		InvocationRegistry: registry,
	})
	items := []model.AgentItem{{ID: "shared", Type: "function_call", Name: "probe", Arguments: `{}`}}
	ownerDone := make(chan []ToolExecutionResult, 1)
	ownerErr := make(chan error, 1)
	go func() {
		results, err := dispatcher.ExecuteToolItems(context.Background(), items)
		if err != nil {
			ownerErr <- err
			return
		}
		ownerDone <- results
	}()
	<-entered
	select {
	case err := <-handlerFailure:
		t.Fatalf("handler could not resolve its live invocation: %v", err)
	default:
	}
	live := <-liveCall
	if live == nil || !live.Active() {
		t.Fatalf("live invocation = %#v", live)
	}

	duplicate, err := dispatcher.ExecuteToolItems(context.Background(), items)
	if err != nil {
		t.Fatalf("duplicate ExecuteToolItems() error = %v", err)
	}
	if len(duplicate) != 1 {
		t.Fatalf("duplicate results = %#v", duplicate)
	}
	if duplicate[0].HandlerExecuted {
		t.Fatal("duplicate call reached its handler")
	}
	if got := duplicate[0].Response.Output.Text(); !strings.Contains(got, "tool call ID shared is already active in this turn") {
		t.Fatalf("duplicate output = %q", got)
	}
	if !live.Active() {
		t.Fatal("duplicate rejection revoked the original invocation")
	}

	close(release)
	select {
	case err := <-ownerErr:
		t.Fatalf("owner ExecuteToolItems() error = %v", err)
	case results := <-ownerDone:
		if len(results) != 1 || results[0].Response.Output.Text() != "ok" {
			t.Fatalf("owner results = %#v", results)
		}
	}
	if _, err := registry.LookupToolInvocation("thread-1", "turn-1", "shared"); !errors.Is(err, agent.ErrToolCallNotActive) {
		t.Fatalf("lookup after dispatch error = %v, want ErrToolCallNotActive", err)
	}
	if live.Active() {
		t.Fatal("invocation stayed active after dispatch")
	}
}

// A dispatcher without the opt-in registry behaves exactly as before and never
// rejects repeated call IDs.
func TestToolDispatcherWithoutInvocationRegistryLikeRust(t *testing.T) {
	toolRegistry := tool.NewRegistry()
	if err := toolRegistry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("probe")}, func(_ context.Context, invocation *tool.Invocation) (*tool.Output, error) {
		return &tool.Output{CallID: invocation.CallID, ToolName: invocation.ToolName, Success: true, Body: "ok"}, nil
	})); err != nil {
		t.Fatalf("register executor: %v", err)
	}
	dispatcher := NewToolDispatcher(&ToolDispatcherOptions{Router: tool.NewRouter(toolRegistry)})
	items := []model.AgentItem{{ID: "shared", Type: "function_call", Name: "probe", Arguments: `{}`}}
	for i := 0; i < 2; i++ {
		results, err := dispatcher.ExecuteToolItems(context.Background(), items)
		if err != nil {
			t.Fatalf("ExecuteToolItems() #%d error = %v", i, err)
		}
		if len(results) != 1 || results[0].Response.Output.Text() != "ok" {
			t.Fatalf("results #%d = %#v", i, results)
		}
	}
}
