package turn

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"codex_go/model"
	"codex_go/tool"
)

func budgetDeniedDispatcher(t *testing.T, calls *atomic.Int64) *ToolDispatcher {
	t.Helper()
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("echo")}, func(context.Context, *tool.Invocation) (*tool.Output, error) {
		calls.Add(1)
		return &tool.Output{Success: true, Body: "ran"}, nil
	})); err != nil {
		t.Fatal(err)
	}
	return NewToolDispatcher(&ToolDispatcherOptions{
		Router:          tool.NewRouter(registry),
		CheckWorkBudget: func() error { return errors.New("budget reached") },
	})
}

func TestToolDispatcherRefusesHandlerAfterBudgetExhaustionLikeRust(t *testing.T) {
	var calls atomic.Int64
	dispatcher := budgetDeniedDispatcher(t, &calls)

	results, err := dispatcher.ExecuteToolItems(context.Background(), []model.AgentItem{{
		Type:      "function_call",
		Name:      "echo",
		CallID:    "call-budget",
		Arguments: `{}`,
	}})
	if err != nil {
		t.Fatalf("ExecuteToolItems() error = %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("handler calls = %d, want 0", calls.Load())
	}
	if len(results) != 1 || !results[0].Aborted || results[0].HandlerExecuted {
		t.Fatalf("results = %#v, want one aborted call before handler start", results)
	}
	if got := results[0].Response.Output.Text(); got != "aborted before handler start: rollout budget exhausted" {
		t.Fatalf("aborted output = %q", got)
	}
}

func TestToolDispatcherAbortToolItemsKeepsHistoryWithoutRunningHandlers(t *testing.T) {
	var calls atomic.Int64
	dispatcher := budgetDeniedDispatcher(t, &calls)

	results := dispatcher.AbortToolItems([]model.AgentItem{{
		Type:      "function_call",
		Name:      "echo",
		CallID:    "call-buffered",
		Arguments: `{}`,
	}}, "aborted before handler start: rollout budget exhausted")
	if calls.Load() != 0 {
		t.Fatalf("handler calls = %d, want 0", calls.Load())
	}
	if len(results) != 1 || results[0].Response == nil || results[0].Response.Type != "function_call_output" {
		t.Fatalf("results = %#v", results)
	}
	if got := results[0].Response.Output.Text(); got != "aborted before handler start: rollout budget exhausted" {
		t.Fatalf("aborted output = %q", got)
	}
}
