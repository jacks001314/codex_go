package turn

import (
	"context"
	"testing"

	"codex_go/model"
)

type modelRequestHookTestAgent struct{}

func (modelRequestHookTestAgent) Run(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
	return &model.AgentResponse{Message: "ok", Usage: model.AgentUsage{InputTokens: 3, OutputTokens: 2, TotalTokens: 5}}, nil
}

func TestAgentLoopObservesModelRequestLifecycleLikeRust(t *testing.T) {
	loop := NewAgentLoop(&AgentLoopOptions{Agent: modelRequestHookTestAgent{}, MaxTurns: 1})
	var events []ModelRequestHookEvent
	_, err := loop.Run(context.Background(), &AgentLoopRequest{
		Prompt:   "hello",
		Model:    "gpt-test",
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		CWD:      "/workspace",
		OnModelRequest: func(_ context.Context, event ModelRequestHookEvent) {
			events = append(events, event)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %#v", events)
	}
	if events[0].Phase != "pre" || events[0].Model != "gpt-test" || events[0].ThreadID != "thread-1" || events[0].TurnID != "turn-1" || events[0].CWD != "/workspace" {
		t.Fatalf("pre event = %#v", events[0])
	}
	if events[1].Phase != "post" || events[1].Status != "completed" || events[1].Usage == nil || events[1].Usage.TotalTokens != 5 {
		t.Fatalf("post event = %#v", events[1])
	}
}
