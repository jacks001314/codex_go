package turn

import (
	"context"
	"strings"
	"testing"

	"codex_go/internal/codexapi"
	"codex_go/internal/model"
	"codex_go/internal/tool"
)

func TestRuntimeInjectsToolsAndRunsLoop(t *testing.T) {
	agent := &runtimeRecordingAgent{}
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{
		Name:        tool.PlainName("echo"),
		Description: "Echo text",
		InputSchema: map[string]any{
			"type": "object",
		},
	}, func(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
		return &tool.Output{Success: true, Body: "ok"}, nil
	})); err != nil {
		t.Fatalf("register echo: %v", err)
	}
	runtime := NewRuntime(&RuntimeOptions{
		Agent:  agent,
		Router: tool.NewRouter(registry),
	})
	attestationProvider := codexapi.NewStaticAttestationProvider("attest")

	result, err := runtime.Run(context.Background(), &AgentLoopRequest{
		Prompt:              "use tool",
		Instructions:        "runtime instructions",
		Originator:          "codex_vscode",
		AttestationProvider: attestationProvider,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Response.Message != "done" || len(result.ToolExecutions) != 1 {
		t.Fatalf("result = %#v", result)
	}
	if len(agent.requests) != 2 || len(agent.requests[0].Tools) != 1 || len(agent.requests[1].Tools) != 1 {
		t.Fatalf("requests = %#v", agent.requests)
	}
	if agent.requests[0].Instructions != "runtime instructions" || agent.requests[1].Instructions != "runtime instructions" {
		t.Fatalf("instructions = %q/%q", agent.requests[0].Instructions, agent.requests[1].Instructions)
	}
	if agent.requests[0].Originator != "codex_vscode" || agent.requests[1].Originator != "codex_vscode" {
		t.Fatalf("originator = %q/%q", agent.requests[0].Originator, agent.requests[1].Originator)
	}
	if agent.requests[0].AttestationProvider != attestationProvider || agent.requests[1].AttestationProvider != attestationProvider {
		t.Fatalf("attestation providers not preserved")
	}
}

func TestRuntimeWithoutRouterPreservesPromptAndResponseInputItems(t *testing.T) {
	agent := &singleTurnAgent{response: &model.AgentResponse{
		Message: "assistant answer",
		Usage:   model.AgentUsage{InputTokens: 2, OutputTokens: 3},
		Items: []model.AgentItem{{
			ID:   "msg-1",
			Type: "agent_message",
			Text: "assistant answer",
		}},
	}}
	runtime := NewRuntime(&RuntimeOptions{Agent: agent})

	result, err := runtime.Run(context.Background(), &AgentLoopRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !resultInputItemsHaveText(result.InputItems, "hello") || !runtimeResultInputItemsHaveAgentText(result.InputItems, "assistant answer") {
		t.Fatalf("result input items = %#v", result.InputItems)
	}
	if result.Iterations != 1 || result.Usage.InputTokens != 2 || result.Usage.OutputTokens != 3 {
		t.Fatalf("result = %#v", result)
	}
	if result.TimingProfile == nil || result.TimingProfile.SamplingRequestCount != 1 {
		t.Fatalf("timing profile = %#v", result.TimingProfile)
	}
	if len(agent.requests) != 1 || agent.requests[0].Prompt != "hello" {
		t.Fatalf("agent requests = %#v", agent.requests)
	}
}

func TestRuntimeWithoutRouterRejectsToolCalls(t *testing.T) {
	agent := &singleTurnAgent{response: &model.AgentResponse{Items: []model.AgentItem{{
		ID:        "call-1",
		Type:      "function_call",
		Name:      "echo",
		CallID:    "call-1",
		Arguments: `{}`,
	}}}}
	runtime := NewRuntime(&RuntimeOptions{Agent: agent})

	_, err := runtime.Run(context.Background(), &AgentLoopRequest{Prompt: "call tool"})
	if err == nil || !strings.Contains(err.Error(), "tool dispatcher is nil") {
		t.Fatalf("Run() error = %v, want dispatcher failure", err)
	}
}

type runtimeRecordingAgent struct {
	requests []model.AgentRequest
}

func (a *runtimeRecordingAgent) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	a.requests = append(a.requests, *request)
	if len(a.requests) == 1 {
		return &model.AgentResponse{Items: []model.AgentItem{{
			ID:        "call-1",
			Type:      "function_call",
			Name:      "echo",
			CallID:    "call-1",
			Arguments: `{}`,
		}}}, nil
	}
	return &model.AgentResponse{
		Message: "done",
		Items:   []model.AgentItem{{ID: "msg-1", Type: "agent_message", Text: "done"}},
	}, nil
}

type singleTurnAgent struct {
	requests []model.AgentRequest
	response *model.AgentResponse
	err      error
}

func (a *singleTurnAgent) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	_ = ctx
	a.requests = append(a.requests, *request)
	if a.err != nil {
		return nil, a.err
	}
	if a.response != nil {
		return a.response, nil
	}
	return &model.AgentResponse{}, nil
}

func runtimeResultInputItemsHaveAgentText(items []any, want string) bool {
	for _, raw := range items {
		item, ok := raw.(*model.AgentItem)
		if ok && item.Text == want {
			return true
		}
	}
	return false
}
