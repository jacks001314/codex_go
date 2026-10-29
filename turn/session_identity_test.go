package turn

import (
	"context"
	"testing"

	"codex_go/model"
	"codex_go/tool"
)

// TestRuntimeFastPathPropagatesSessionIDToModelRequest covers the no-router
// sampling path. A host-owned internal session reuses its parent's Responses
// session while keeping its own thread id (Rust #39994).
func TestRuntimeFastPathPropagatesSessionIDToModelRequest(t *testing.T) {
	agent := &singleTurnAgent{response: &model.AgentResponse{Message: "done"}}
	runtime := NewRuntime(&RuntimeOptions{Agent: agent})

	if _, err := runtime.Run(context.Background(), &AgentLoopRequest{
		Prompt:    "review",
		ThreadID:  "internal-thread",
		SessionID: "parent-session",
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(agent.requests) != 1 {
		t.Fatalf("agent requests = %d, want 1", len(agent.requests))
	}
	if got := agent.requests[0].SessionID; got != "parent-session" {
		t.Fatalf("model request session id = %q, want parent-session", got)
	}
	if got := agent.requests[0].ThreadID; got != "internal-thread" {
		t.Fatalf("model request thread id = %q, want internal-thread", got)
	}
}

// TestAgentLoopPropagatesSessionIDToModelRequests covers the tool-loop path,
// which builds one model request per sampling step.
func TestAgentLoopPropagatesSessionIDToModelRequests(t *testing.T) {
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
	runtime := NewRuntime(&RuntimeOptions{Agent: agent, Router: tool.NewRouter(registry)})

	if _, err := runtime.Run(context.Background(), &AgentLoopRequest{
		Prompt:    "review",
		ThreadID:  "internal-thread",
		SessionID: "parent-session",
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(agent.requests) != 2 {
		t.Fatalf("agent requests = %d, want 2", len(agent.requests))
	}
	for i, request := range agent.requests {
		if got := request.SessionID; got != "parent-session" {
			t.Fatalf("request %d session id = %q, want parent-session", i, got)
		}
		if got := request.ThreadID; got != "internal-thread" {
			t.Fatalf("request %d thread id = %q, want internal-thread", i, got)
		}
	}
}
