package turn

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"codex_go/model"
	"codex_go/tool"
)

type settleOrderLoopAgent struct {
	calls int
}

func (a *settleOrderLoopAgent) Run(_ context.Context, _ *model.AgentRequest) (*model.AgentResponse, error) {
	a.calls++
	if a.calls == 1 {
		return &model.AgentResponse{
			ResponseID: "resp-tool",
			Usage:      model.AgentUsage{OutputTokens: 7},
			Items: []model.AgentItem{{
				Type: "function_call", CallID: "call-order", Name: "echo", Arguments: `{}`,
			}},
		}, nil
	}
	return &model.AgentResponse{
		ResponseID: "resp-done",
		Message:    "done",
		Items:      []model.AgentItem{{Type: "agent_message", Text: "done"}},
	}, nil
}

func TestAgentLoopSettlesResponseBeforeToolDispatchLikeRust(t *testing.T) {
	var order []string
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("echo")}, func(context.Context, *tool.Invocation) (*tool.Output, error) {
		order = append(order, "tool")
		return &tool.Output{Success: true, Body: "ran"}, nil
	})); err != nil {
		t.Fatal(err)
	}
	loop := NewAgentLoop(&AgentLoopOptions{
		Agent:      &settleOrderLoopAgent{},
		Dispatcher: NewToolDispatcher(&ToolDispatcherOptions{Router: tool.NewRouter(registry)}),
	})
	settled := false
	result, err := loop.Run(context.Background(), &AgentLoopRequest{
		Prompt:                   "run",
		SettleBeforeToolDispatch: true,
		SamplingFollowUp: func(ctx *SamplingFollowUpContext) []any {
			if !settled && ctx != nil && ctx.HasToolCalls {
				settled = true
				order = append(order, "followup")
				return []any{model.DeveloperMessageInputItem("settled before tools")}
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0] != "followup" || order[1] != "tool" {
		t.Fatalf("order = %#v, want followup before tool", order)
	}
	if result.SamplingFollowUps != 1 {
		t.Fatalf("SamplingFollowUps = %d, want 1", result.SamplingFollowUps)
	}
}

func TestAgentLoopAbortsBufferedToolsWhenSettlementExhaustsBudgetLikeRust(t *testing.T) {
	var exhausted atomic.Bool
	var calls atomic.Int64
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("echo")}, func(context.Context, *tool.Invocation) (*tool.Output, error) {
		calls.Add(1)
		return &tool.Output{Success: true, Body: "ran"}, nil
	})); err != nil {
		t.Fatal(err)
	}
	budgetErr := errors.New("budget reached")
	loop := NewAgentLoop(&AgentLoopOptions{
		Agent: &settleOrderLoopAgent{},
		Dispatcher: NewToolDispatcher(&ToolDispatcherOptions{
			Router: tool.NewRouter(registry),
			CheckWorkBudget: func() error {
				if exhausted.Load() {
					return budgetErr
				}
				return nil
			},
		}),
	})
	result, err := loop.Run(context.Background(), &AgentLoopRequest{
		Prompt:                   "run",
		SettleBeforeToolDispatch: true,
		CheckWorkBudget: func() error {
			if exhausted.Load() {
				return budgetErr
			}
			return nil
		},
		SamplingFollowUp: func(ctx *SamplingFollowUpContext) []any {
			if ctx != nil && ctx.HasToolCalls {
				exhausted.Store(true)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v, want the settled response preserved", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("tool handler calls = %d, want 0", calls.Load())
	}
	if len(result.ToolExecutions) != 1 || !result.ToolExecutions[0].Aborted {
		t.Fatalf("tool executions = %#v, want one aborted call", result.ToolExecutions)
	}
	found := false
	for _, item := range result.InputItems {
		if output, ok := item.(*ToolResponseItem); ok && output.Output.Text() == "aborted before handler start: rollout budget exhausted" {
			found = true
		}
	}
	if !found {
		t.Fatalf("aborted tool output missing from input items: %#v", result.InputItems)
	}
}
