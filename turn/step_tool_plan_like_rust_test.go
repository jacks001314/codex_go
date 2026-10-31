package turn

import (
	"context"
	"errors"
	"sync"
	"testing"

	"codex_go/model"
	"codex_go/tool"
)

var errStepToolPlanUnavailable = errors.New("step tool plan unavailable")

// stepToolPlanAgent asks for `echo` on the first step and for a tool that only
// exists in the second step's rebuilt plan on the second step.
type stepToolPlanAgent struct {
	requests []model.AgentRequest
}

func (a *stepToolPlanAgent) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	_ = ctx
	a.requests = append(a.requests, *request)
	switch len(a.requests) {
	case 1:
		return &model.AgentResponse{Items: []model.AgentItem{{
			ID:        "call-1",
			Type:      "function_call",
			Name:      "echo",
			CallID:    "call-1",
			Arguments: `{}`,
		}}}, nil
	case 2:
		return &model.AgentResponse{Items: []model.AgentItem{{
			ID:        "call-2",
			Type:      "function_call",
			Name:      "step_only",
			CallID:    "call-2",
			Arguments: `{}`,
		}}}, nil
	case 3:
		return &model.AgentResponse{Items: []model.AgentItem{{
			ID:        "call-3",
			Type:      "function_call",
			Name:      "echo",
			CallID:    "call-3",
			Arguments: `{}`,
		}}}, nil
	}
	return &model.AgentResponse{
		Message: "done",
		Items:   []model.AgentItem{{ID: "msg-1", Type: "agent_message", Text: "done"}},
	}, nil
}

func stepToolPlanExecutor(name string, calls *int) tool.Executor {
	return tool.NewExecutorFunc(tool.Spec{
		Name:        tool.PlainName(name),
		Description: name,
		InputSchema: map[string]any{"type": "object"},
	}, func(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
		_ = ctx
		_ = invocation
		*calls++
		return &tool.Output{Success: true, Body: name + " ok"}, nil
	})
}

// Rust #44242 plans every sampling step from that step's captured settings:
// the step's tool router replaces the turn's for both the advertised tools and
// dispatch, and a plan rebuild that fails keeps the previous one.
func TestRuntimeRebuildsStepToolPlanLikeRust(t *testing.T) {
	turnCalls := 0
	turnRegistry := tool.NewRegistry()
	if err := turnRegistry.Register(stepToolPlanExecutor("echo", &turnCalls)); err != nil {
		t.Fatalf("register turn echo: %v", err)
	}
	stepEchoCalls := 0
	stepRegistry := tool.NewRegistry()
	if err := stepRegistry.Register(stepToolPlanExecutor("echo", &stepEchoCalls)); err != nil {
		t.Fatalf("register step echo: %v", err)
	}
	stepOnlyCalls := 0
	if err := stepRegistry.Register(stepToolPlanExecutor("step_only", &stepOnlyCalls)); err != nil {
		t.Fatalf("register step_only: %v", err)
	}
	var mu sync.Mutex
	var planned []string
	agent := &stepToolPlanAgent{}
	runtime := NewRuntime(&RuntimeOptions{
		Agent:  agent,
		Router: tool.NewRouter(turnRegistry),
		StepToolPlan: func(modelID string) (*StepToolPlan, error) {
			mu.Lock()
			defer mu.Unlock()
			planned = append(planned, modelID)
			if modelID == "turn-model" {
				return &StepToolPlan{Router: tool.NewRouter(turnRegistry)}, nil
			}
			return &StepToolPlan{Router: tool.NewRouter(stepRegistry)}, nil
		},
	})
	// The turn starts on its own model, then switches to a step model and back.
	stepModels := []string{"step-model", "turn-model"}
	stepModelCalls := 0
	if _, err := runtime.Run(context.Background(), &AgentLoopRequest{
		Prompt: "use tools",
		Model:  "turn-model",
		StepSettings: func() *AgentStepSettings {
			model := "turn-model"
			if stepModelCalls < len(stepModels) {
				model = stepModels[stepModelCalls]
			}
			stepModelCalls++
			return &AgentStepSettings{Model: model}
		},
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(agent.requests) != 4 {
		t.Fatalf("requests = %d, want 4", len(agent.requests))
	}
	if !runtimeRequestToolNames(agent.requests[0].Tools)["echo"] || runtimeRequestToolNames(agent.requests[0].Tools)["step_only"] {
		t.Fatalf("first step tools = %#v, want only the turn plan", runtimeRequestToolNames(agent.requests[0].Tools))
	}
	if !runtimeRequestToolNames(agent.requests[1].Tools)["step_only"] {
		t.Fatalf("second step tools = %#v, want the rebuilt plan", runtimeRequestToolNames(agent.requests[1].Tools))
	}
	if runtimeRequestToolNames(agent.requests[2].Tools)["step_only"] {
		t.Fatalf("third step tools = %#v, want the turn's plan restored", runtimeRequestToolNames(agent.requests[2].Tools))
	}
	mu.Lock()
	plannedModels := append([]string(nil), planned...)
	mu.Unlock()
	if len(plannedModels) != 2 || plannedModels[0] != "step-model" || plannedModels[1] != "turn-model" {
		t.Fatalf("step plan rebuilds = %#v, want both model changes", plannedModels)
	}
	if turnCalls != 2 {
		t.Fatalf("turn-plan dispatch calls = %d, want the first and third steps", turnCalls)
	}
	if stepOnlyCalls != 1 || stepEchoCalls != 0 {
		t.Fatalf("step-plan dispatch calls = step_only %d, echo %d; want the rebuilt router", stepOnlyCalls, stepEchoCalls)
	}
}

// repeatedEchoAgent asks the same tool on each of its two steps.
type repeatedEchoAgent struct {
	requests []model.AgentRequest
}

func (a *repeatedEchoAgent) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	_ = ctx
	a.requests = append(a.requests, *request)
	if len(a.requests) <= 2 {
		callID := "echo-" + string(rune('0'+len(a.requests)))
		return &model.AgentResponse{Items: []model.AgentItem{{
			ID:        callID,
			Type:      "function_call",
			Name:      "echo",
			CallID:    callID,
			Arguments: `{}`,
		}}}, nil
	}
	return &model.AgentResponse{
		Message: "done",
		Items:   []model.AgentItem{{ID: "msg-1", Type: "agent_message", Text: "done"}},
	}, nil
}

// A failed rebuild must not drop the turn's tools: the step still samples with
// the previous plan.
func TestRuntimeKeepsPreviousPlanWhenStepRebuildFailsLikeRust(t *testing.T) {
	calls := 0
	registry := tool.NewRegistry()
	if err := registry.Register(stepToolPlanExecutor("echo", &calls)); err != nil {
		t.Fatalf("register echo: %v", err)
	}
	agent := &repeatedEchoAgent{}
	runtime := NewRuntime(&RuntimeOptions{
		Agent:  agent,
		Router: tool.NewRouter(registry),
		StepToolPlan: func(modelID string) (*StepToolPlan, error) {
			return nil, errStepToolPlanUnavailable
		},
	})
	if _, err := runtime.Run(context.Background(), &AgentLoopRequest{
		Prompt:       "use tool",
		Model:        "turn-model",
		StepSettings: func() *AgentStepSettings { return &AgentStepSettings{Model: "step-model"} },
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !runtimeRequestToolNames(agent.requests[1].Tools)["echo"] {
		t.Fatalf("second step tools = %#v, want the turn plan kept", runtimeRequestToolNames(agent.requests[1].Tools))
	}
	if calls != 2 {
		t.Fatalf("dispatch calls = %d, want the turn plan to serve both steps", calls)
	}
}
