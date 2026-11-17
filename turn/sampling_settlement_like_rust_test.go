package turn

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"codex_go/model"
	"codex_go/tool"
)

// perResponseUsageAgent answers with a tool call first and a final message
// second, reporting a distinct usage for each response.
type perResponseUsageAgent struct {
	usages []model.AgentUsage
	calls  int
}

func (a *perResponseUsageAgent) Run(_ context.Context, _ *model.AgentRequest) (*model.AgentResponse, error) {
	usage := model.AgentUsage{}
	if a.calls < len(a.usages) {
		usage = a.usages[a.calls]
	}
	a.calls++
	if a.calls == 1 {
		return &model.AgentResponse{
			ResponseID: "resp-settle-tool",
			Usage:      usage,
			Items: []model.AgentItem{{
				Type: "function_call", CallID: "call-settle", Name: "echo", Arguments: `{}`,
			}},
		}, nil
	}
	return &model.AgentResponse{
		ResponseID: "resp-settle-done",
		Message:    "done",
		Usage:      usage,
		Items:      []model.AgentItem{{Type: "agent_message", Text: "done"}},
	}, nil
}

func newSettlementTestLoop(t *testing.T, agent model.AgentRunner, toolCalls *int) *AgentLoop {
	t.Helper()
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("echo")}, func(context.Context, *tool.Invocation) (*tool.Output, error) {
		if toolCalls != nil {
			*toolCalls = *toolCalls + 1
		}
		return &tool.Output{Success: true, Body: "ran"}, nil
	})); err != nil {
		t.Fatal(err)
	}
	return NewAgentLoop(&AgentLoopOptions{
		Agent:      agent,
		Dispatcher: NewToolDispatcher(&ToolDispatcherOptions{Router: tool.NewRouter(registry)}),
	})
}

// Rust #61958 settles every completed response with its own token usage; Go
// used to hand the cumulative turn total to the budget, charging earlier
// responses again on each iteration.
func TestAgentLoopSettlesEachResponseWithItsOwnUsageLikeRust(t *testing.T) {
	agent := &perResponseUsageAgent{usages: []model.AgentUsage{{OutputTokens: 7}, {OutputTokens: 5}}}
	loop := newSettlementTestLoop(t, agent, nil)
	var settled []int64
	result, err := loop.Run(context.Background(), &AgentLoopRequest{
		Prompt: "run",
		SamplingSettlement: func(ctx *SamplingFollowUpContext) error {
			settled = append(settled, ctx.Usage.OutputTokens)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !reflect.DeepEqual(settled, []int64{7, 5}) {
		t.Fatalf("settled usages = %#v, want each response charged once", settled)
	}
	if result.Usage.OutputTokens != 12 {
		t.Fatalf("turn usage = %#v, want the accumulated 12", result.Usage)
	}
}

// Rust #61958: an accounting failure keeps the completed response's output and
// usage in the turn result, refuses the response's buffered tools, and reports
// the failure instead of discarding the response.
func TestAgentLoopKeepsSettledResponseWhenAccountingFailsLikeRust(t *testing.T) {
	budgetErr := errors.New("session budget exceeded")
	var toolCalls int
	loop := newSettlementTestLoop(t, &settleOrderLoopAgent{}, &toolCalls)
	result, err := loop.Run(context.Background(), &AgentLoopRequest{
		Prompt: "run",
		SamplingSettlement: func(ctx *SamplingFollowUpContext) error {
			if ctx != nil && ctx.HasToolCalls {
				return budgetErr
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v, want the failure carried in the result", err)
	}
	if !errors.Is(result.SamplingError, budgetErr) {
		t.Fatalf("SamplingError = %v, want the accounting failure", result.SamplingError)
	}
	if toolCalls != 0 {
		t.Fatalf("tool handler calls = %d, want 0 after the accounting failure", toolCalls)
	}
	if len(result.ToolExecutions) != 1 || !result.ToolExecutions[0].Aborted || result.ToolExecutions[0].HandlerExecuted {
		t.Fatalf("tool executions = %#v, want one aborted call whose handler never ran", result.ToolExecutions)
	}
	if result.Usage.OutputTokens != 7 {
		t.Fatalf("usage = %#v, want the completed response's 7 tokens", result.Usage)
	}
	found := false
	for _, item := range result.InputItems {
		if functionCall, ok := item.(*model.AgentItem); ok && functionCall.CallID == "call-order" {
			found = true
		}
	}
	if !found {
		t.Fatalf("input items = %#v, want the completed tool call preserved", result.InputItems)
	}
}
