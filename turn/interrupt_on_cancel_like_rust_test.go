package turn

import (
	"context"
	"errors"
	"testing"

	"codex_go/model"
	"codex_go/tool"
)

type canceledResponseLoopAgent struct {
	requests []model.AgentRequest
}

func (a *canceledResponseLoopAgent) Run(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	a.requests = append(a.requests, *request)
	return &model.AgentResponse{
		Canceled:   true,
		ResponseID: "resp-cancel",
		Usage:      model.AgentUsage{InputTokens: 5, OutputTokens: 3, TotalTokens: 8},
		Items:      []model.AgentItem{{ID: "note", Type: "agent_message", Text: "partial answer"}},
	}, nil
}

// Rust #57864: a cancelled Responses Lite sampling request that interrupted
// upstream still charges the completed usage and ends the turn as aborted
// instead of continuing with a follow-up request.
func TestAgentLoopChargesCanceledInterruptedResponseLikeRust(t *testing.T) {
	agent := &canceledResponseLoopAgent{}
	loop := NewAgentLoop(&AgentLoopOptions{
		Agent:      agent,
		Dispatcher: NewToolDispatcher(&ToolDispatcherOptions{Router: tool.NewRouter(tool.NewRegistry())}),
	})
	var charged []model.AgentUsage
	_, err := loop.Run(context.Background(), &AgentLoopRequest{
		Prompt: "answer this", Model: "gpt-test", ThreadID: "thread-1", TurnID: "turn-1",
		InterruptOnCancel: true,
		OnIncompleteResponseUsage: func(usage model.AgentUsage) {
			charged = append(charged, usage)
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
	if len(agent.requests) != 1 {
		t.Fatalf("sampling requests = %d, want 1 (no follow-up after cancel)", len(agent.requests))
	}
	if !agent.requests[0].InterruptOnCancel {
		t.Fatalf("sampling request did not carry interrupt_on_cancel")
	}
	if len(charged) != 1 || charged[0].TotalTokens != 8 {
		t.Fatalf("charged usage = %#v, want the completed 8 tokens", charged)
	}
}
