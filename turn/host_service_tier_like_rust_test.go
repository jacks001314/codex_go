package turn

import (
	"context"
	"errors"
	"testing"

	"codex_go/tool"
)

// Exercise the real sampling loop: an async host capture changes the next
// emitted request, and cancellation stops it before another model invocation.
func TestAgentLoopAsyncServiceTierStepCaptureLikeRust(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "accepted", true: "cancelled"}[cancelled], func(t *testing.T) {
			agent := &fakeLoopAgent{}
			registry := tool.NewRegistry()
			if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("echo")}, func(context.Context, *tool.Invocation) (*tool.Output, error) {
				return &tool.Output{Success: true, Body: "ok"}, nil
			})); err != nil {
				t.Fatal(err)
			}
			loop := NewAgentLoop(&AgentLoopOptions{Agent: agent, Dispatcher: NewToolDispatcher(&ToolDispatcherOptions{Router: tool.NewRouter(registry)}), MaxTurns: 3})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			_, err := loop.Run(ctx, &AgentLoopRequest{
				Prompt: "run echo", Model: "initial", ServiceTier: "priority",
				StepSettings: func() *AgentStepSettings { t.Fatal("sync fallback called"); return nil },
				StepSettingsContext: func(ctx context.Context) (*AgentStepSettings, error) {
					if cancelled {
						cancel()
						<-ctx.Done()
						return nil, ctx.Err()
					}
					return &AgentStepSettings{Model: "updated", ServiceTier: "flex"}, nil
				},
			})
			if cancelled {
				if !errors.Is(err, context.Canceled) || len(agent.requests) != 1 {
					t.Fatalf("cancellation=%v,requests=%d", err, len(agent.requests))
				}
			} else if err != nil || len(agent.requests) != 2 || agent.requests[1].ServiceTier != "flex" {
				t.Fatalf("request tier not refreshed: %#v, %v", agent.requests, err)
			}
		})
	}
}
