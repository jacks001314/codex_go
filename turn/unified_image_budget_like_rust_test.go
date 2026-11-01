package turn

import (
	"context"
	"testing"

	"codex_go/model"
	"codex_go/tool"
)

type unifiedBudgetCaptureAgent struct{ requests []model.AgentRequest }

func (a *unifiedBudgetCaptureAgent) Run(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	a.requests = append(a.requests, *request)
	return &model.AgentResponse{ResponseID: "resp", Message: "done", Items: []model.AgentItem{{Type: "agent_message", Text: "done"}}}, nil
}

// Rust selects ImagePreparationMode from unified_image_budget_enabled for the
// sampling model, so the sampling request must carry the flag per step.
func TestSamplingRequestCarriesUnifiedImageBudgetLikeRust(t *testing.T) {
	agent := &unifiedBudgetCaptureAgent{}
	runtime := NewRuntime(&RuntimeOptions{
		Agent:              agent,
		Router:             tool.NewRouter(tool.NewRegistry()),
		UnifiedImageBudget: true,
		UnifiedImageBudgetForModel: func(modelID string) bool {
			return modelID == "gpt-unified"
		},
	})
	if _, err := runtime.Run(context.Background(), &AgentLoopRequest{
		Prompt: "hi", ThreadID: "t1", TurnID: "turn-1", Model: "gpt-unified", ToolMode: model.ToolModeCodeMode,
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(agent.requests) != 1 || !agent.requests[0].UnifiedImageBudget {
		t.Fatalf("sampling request = %#v, want UnifiedImageBudget", agent.requests)
	}

	agent = &unifiedBudgetCaptureAgent{}
	runtime = NewRuntime(&RuntimeOptions{
		Agent:              agent,
		Router:             tool.NewRouter(tool.NewRegistry()),
		UnifiedImageBudget: true,
		UnifiedImageBudgetForModel: func(modelID string) bool {
			return modelID == "gpt-unified"
		},
	})
	if _, err := runtime.Run(context.Background(), &AgentLoopRequest{
		Prompt: "hi", ThreadID: "t2", TurnID: "turn-2", Model: "gpt-plain", ToolMode: model.ToolModeCodeMode,
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(agent.requests) != 1 || agent.requests[0].UnifiedImageBudget {
		t.Fatalf("sampling request = %#v, want no unified budget", agent.requests)
	}
}
