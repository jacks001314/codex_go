package turn

import (
	"context"
	"testing"

	"codex_go/model"
	"codex_go/tool"
)

// modelPreemptingLoopAgent reports a preempted sampling step on the first
// request while queueing new user input, then answers normally: the Go
// counterpart of Rust's held-tool scenario for #48141, where the model request
// itself is interrupted.
type modelPreemptingLoopAgent struct {
	t        *testing.T
	mailbox  *SteerMailbox
	requests []model.AgentRequest
}

func (a *modelPreemptingLoopAgent) Run(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	a.requests = append(a.requests, *request)
	if request.Preempt == nil {
		a.t.Error("the sampling request did not carry the step's preemption signal")
	}
	if len(a.requests) == 1 {
		if err := a.mailbox.Enqueue(&SteerEnqueueParams{
			ThreadID:   "t1",
			TurnID:     "turn-1",
			InputItems: []any{queuedUserMessage("answer this instead")},
		}); err != nil {
			return nil, err
		}
		return &model.AgentResponse{Preempted: true, ResponseID: "resp-preempted"}, nil
	}
	return &model.AgentResponse{
		ResponseID: "resp-done",
		Message:    "done",
		Items:      []model.AgentItem{{Type: "agent_message", Text: "done"}},
	}, nil
}

// Mirrors Rust #48141: a preempted sampling step produces no assistant output,
// the replacement request sends full history, and the queued user input reaches
// the model on the next step.
func TestAgentLoopModelPreemptionDeliversQueuedInput(t *testing.T) {
	mailbox := NewSteerMailbox()
	agent := &modelPreemptingLoopAgent{t: t, mailbox: mailbox}
	runtime := NewRuntime(&RuntimeOptions{
		Agent:            agent,
		Router:           tool.NewRouter(tool.NewRegistry()),
		SteerMailbox:     mailbox,
		InstantInterrupt: true,
	})
	result, err := runtime.Run(context.Background(), &AgentLoopRequest{
		Prompt: "go", ThreadID: "t1", TurnID: "turn-1",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(agent.requests) != 2 {
		t.Fatalf("sampling requests = %d, want the preempted step and its replacement", len(agent.requests))
	}
	// The preempted step left no assistant response behind: only the replacement
	// request's output is recorded.
	if len(result.Responses) != 1 || result.Responses[0].ResponseID != "resp-done" {
		t.Fatalf("responses = %#v", result.Responses)
	}
	if result.Response == nil || result.Response.Message != "done" {
		t.Fatalf("final response = %#v", result.Response)
	}
	// The replacement request starts a fresh continuation, so it carries the full
	// history instead of a previous_response_id.
	if got := agent.requests[1].PreviousResponseID; got != "" {
		t.Fatalf("the replacement request continued from %q, want full history", got)
	}
	// The original input is preserved for the replacement request (Go carries the
	// turn prompt separately from the accumulated history).
	if agent.requests[1].Prompt != "go" {
		t.Fatalf("the replacement request lost the original input: %#v", agent.requests[1].Prompt)
	}
	if !inputItemsContainText(agent.requests[1].InputItems, "answer this instead") {
		t.Fatalf("the queued user input did not reach the replacement request: %#v", agent.requests[1].InputItems)
	}
	if !inputItemsContainText(result.SteerInputItems, "answer this instead") {
		t.Fatalf("result steer items = %#v", result.SteerInputItems)
	}
}

// Without the feature the step has no preemption signal at all, so a request
// that reports itself preempted is not expected (the runner would never do so);
// this pins the gate the loop applies.
func TestAgentLoopWithoutInstantInterruptPassesNoPreemptSignal(t *testing.T) {
	mailbox := NewSteerMailbox()
	agent := &countingPreemptSignalAgent{}
	runtime := NewRuntime(&RuntimeOptions{
		Agent:            agent,
		Router:           tool.NewRouter(tool.NewRegistry()),
		SteerMailbox:     mailbox,
		InstantInterrupt: false,
	})
	if _, err := runtime.Run(context.Background(), &AgentLoopRequest{Prompt: "go", ThreadID: "t1", TurnID: "turn-1"}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if agent.requests != 1 {
		t.Fatalf("sampling requests = %d", agent.requests)
	}
	if agent.signalsSeen != 0 {
		t.Fatalf("preempt signals seen = %d, want none", agent.signalsSeen)
	}
}

type countingPreemptSignalAgent struct {
	requests    int
	signalsSeen int
}

func (a *countingPreemptSignalAgent) Run(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	a.requests++
	if request.Preempt != nil {
		a.signalsSeen++
	}
	return &model.AgentResponse{ResponseID: "resp-done", Message: "done", Items: []model.AgentItem{{Type: "agent_message", Text: "done"}}}, nil
}
