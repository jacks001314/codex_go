package turn

import (
	"context"
	"testing"

	"codex_go/model"
)

// modelRequestHookTestAgent reports one attempt through the request's model
// hook, mirroring what ResponsesAgentRunner does per opened response stream.
type modelRequestHookTestAgent struct{}

func (modelRequestHookTestAgent) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	if request.OnModelRequest != nil {
		request.OnModelRequest(ctx, model.ModelRequestHookEvent{
			Phase: "pre", Kind: model.SamplingKindSampling, RequestID: request.RequestID, AttemptNumber: 1, Model: request.Model, TurnID: request.TurnID,
		})
		usage := model.AgentUsage{InputTokens: 3, OutputTokens: 2, TotalTokens: 5}
		request.OnModelRequest(ctx, model.ModelRequestHookEvent{
			Phase: "post", Status: "completed", Kind: model.SamplingKindSampling, RequestID: request.RequestID, AttemptNumber: 1,
			Model: request.Model, TurnID: request.TurnID, ResponseID: "resp-1", Usage: &usage,
		})
	}
	return &model.AgentResponse{Message: "ok", ResponseID: "resp-1", Usage: model.AgentUsage{InputTokens: 3, OutputTokens: 2, TotalTokens: 5}}, nil
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
		OnModelRequest: func(_ context.Context, event ModelRequestHookEvent) error {
			events = append(events, event)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %#v", events)
	}
	for _, event := range events {
		if event.ThreadID != "thread-1" || event.TurnID != "turn-1" || event.CWD != "/workspace" {
			t.Fatalf("event identity = %#v", event)
		}
		if event.Kind != "sampling" || event.RequestID == "" || event.AttemptNumber != 1 {
			t.Fatalf("event attempt identity = %#v", event)
		}
	}
	if events[0].Phase != "pre" || events[0].Model != "gpt-test" {
		t.Fatalf("pre event = %#v", events[0])
	}
	if events[1].Phase != "post" || events[1].Status != "completed" || events[1].Usage == nil || events[1].Usage.TotalTokens != 5 || events[1].ResponseID != "resp-1" {
		t.Fatalf("post event = %#v", events[1])
	}
}
