package turn

import (
	"context"
	"sync"
	"testing"

	"codex_go/tool"
)

// Rust #44242 plans every sampling step's tools from that step's captured
// settings. Go finalizes the registry once per turn, so the runtime asks the
// host to refresh the model-dependent tool surface before building a later
// step's tool list.
func TestRuntimeRefreshesStepToolSurfaceForLaterStepsLikeRust(t *testing.T) {
	agent := &runtimeRecordingAgent{}
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{
		Name:        tool.PlainName("echo"),
		Description: "Echo text",
		InputSchema: map[string]any{"type": "object"},
	}, func(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
		return &tool.Output{Success: true, Body: "ok"}, nil
	})); err != nil {
		t.Fatalf("register echo: %v", err)
	}
	var mu sync.Mutex
	var refreshed []string
	runtime := NewRuntime(&RuntimeOptions{
		Agent:  agent,
		Router: tool.NewRouter(registry),
		StepToolSurface: func(model string) {
			mu.Lock()
			defer mu.Unlock()
			refreshed = append(refreshed, model)
		},
	})
	if _, err := runtime.Run(context.Background(), &AgentLoopRequest{
		Prompt:       "use tool",
		Model:        "turn-start-model",
		StepSettings: func() *AgentStepSettings { return &AgentStepSettings{Model: "step-two-model"} },
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	mu.Lock()
	refreshCalls := append([]string(nil), refreshed...)
	mu.Unlock()
	if len(refreshCalls) != 1 || refreshCalls[0] != "step-two-model" {
		t.Fatalf("step tool surface refreshes = %#v, want the second step's model", refreshCalls)
	}
	if len(agent.requests) != 2 || agent.requests[1].Model != "step-two-model" {
		t.Fatalf("step models = %#v", agent.requests)
	}
}
