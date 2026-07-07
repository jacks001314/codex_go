package turn

import (
	"context"
	"testing"
	"time"

	"codex_go/internal/model"
	"codex_go/internal/tool"
)

func TestAgentLoopRunsToolsAndContinuesSampling(t *testing.T) {
	agent := &fakeLoopAgent{}
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("echo")}, func(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
		return &tool.Output{Success: true, Body: "tool result"}, nil
	})); err != nil {
		t.Fatalf("register echo: %v", err)
	}
	loop := NewAgentLoop(&AgentLoopOptions{
		Agent:      agent,
		Dispatcher: NewToolDispatcher(&ToolDispatcherOptions{Router: tool.NewRouter(registry)}),
		MaxTurns:   3,
	})

	result, err := loop.Run(context.Background(), &AgentLoopRequest{
		Prompt:             "run echo",
		Instructions:       "custom base instructions",
		Model:              "gpt-test",
		Originator:         "codex_app_server",
		Store:              true,
		PreviousResponseID: "resp-prev",
		ServiceTier:        "priority",
		PromptCacheKey:     "cache-key",
		ClientMetadata:     map[string]string{"thread_id": "thread-1"},
		OutputSchema:       map[string]any{"type": "object"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Response.Message != "done" || result.Iterations != 2 || len(result.ToolExecutions) != 1 {
		t.Fatalf("result = %#v", result)
	}
	if result.Usage.InputTokens != 11 || result.Usage.CachedInputTokens != 1 || result.Usage.OutputTokens != 7 || result.Usage.ReasoningOutputTokens != 2 || result.Usage.TotalTokens != 18 {
		t.Fatalf("usage = %#v", result.Usage)
	}
	if result.TimingProfile == nil || result.TimingProfile.SamplingRequestCount != 2 {
		t.Fatalf("timing profile = %#v", result.TimingProfile)
	}
	if len(agent.requests) != 2 || len(agent.requests[1].InputItems) != 3 {
		t.Fatalf("agent requests = %#v", agent.requests)
	}
	if !agent.requests[0].Store || agent.requests[0].Originator != "codex_app_server" || agent.requests[0].PreviousResponseID != "resp-prev" || agent.requests[0].ServiceTier != "priority" || agent.requests[0].PromptCacheKey != "cache-key" || agent.requests[0].ClientMetadata["thread_id"] != "thread-1" {
		t.Fatalf("request controls = %#v", agent.requests[0])
	}
	if agent.requests[1].PreviousResponseID != "resp-tool" {
		t.Fatalf("second request previous response id = %q", agent.requests[1].PreviousResponseID)
	}
	if agent.requests[0].Instructions != "custom base instructions" || agent.requests[1].Instructions != "custom base instructions" {
		t.Fatalf("instructions = %q/%q", agent.requests[0].Instructions, agent.requests[1].Instructions)
	}
	if schema, ok := agent.requests[0].OutputSchema.(map[string]any); !ok || schema["type"] != "object" {
		t.Fatalf("output schema = %#v", agent.requests[0].OutputSchema)
	}
	call, ok := agent.requests[1].InputItems[1].(*model.AgentItem)
	if !ok || call.Type != "function_call" || call.CallID != "call-1" {
		t.Fatalf("tool call input = %#v", agent.requests[1].InputItems[1])
	}
	item, ok := agent.requests[1].InputItems[2].(*ToolResponseItem)
	if !ok || item.Type != "function_call_output" || item.Output.Text() != "tool result" {
		t.Fatalf("tool output input = %#v", agent.requests[1].InputItems[2])
	}
}

func TestAgentLoopRecordsTTFTFromStreamingEvent(t *testing.T) {
	start := time.Unix(1800000000, 0)
	now := start
	agent := &streamTimingLoopAgent{advance: func(delta time.Duration) {
		now = start.Add(delta)
	}}
	loop := NewAgentLoop(&AgentLoopOptions{
		Agent: agent,
		Now: func() time.Time {
			return now
		},
	})
	timing := NewTimingState()

	result, err := loop.Run(context.Background(), &AgentLoopRequest{
		Prompt: "stream hello",
		Timing: timing,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.TimingProfile == nil || result.TimingProfile.SamplingRequestCount != 1 {
		t.Fatalf("timing profile = %#v", result.TimingProfile)
	}
	ttft, ok := timing.TimeToFirstToken()
	if !ok || ttft != 120*time.Millisecond {
		t.Fatalf("TTFT = %s/%v, want 120ms/true", ttft, ok)
	}
}

func TestAgentLoopAllowsEmptyInput(t *testing.T) {
	agent := &emptyInputLoopAgent{}
	loop := NewAgentLoop(&AgentLoopOptions{Agent: agent})

	result, err := loop.Run(context.Background(), &AgentLoopRequest{})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Response.Message != "done" || len(agent.requests) != 1 {
		t.Fatalf("result = %#v requests = %#v", result, agent.requests)
	}
	if agent.requests[0].Prompt != "" || len(agent.requests[0].InputItems) != 0 {
		t.Fatalf("agent request = %#v", agent.requests[0])
	}
}

func TestAgentLoopStopsAtIterationLimit(t *testing.T) {
	agent := &fakeLoopAgent{alwaysTool: true}
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("echo")}, func(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
		return &tool.Output{Success: true, Body: "again"}, nil
	})); err != nil {
		t.Fatalf("register echo: %v", err)
	}
	loop := NewAgentLoop(&AgentLoopOptions{
		Agent:      agent,
		Dispatcher: NewToolDispatcher(&ToolDispatcherOptions{Router: tool.NewRouter(registry)}),
		MaxTurns:   2,
	})

	_, err := loop.Run(context.Background(), &AgentLoopRequest{Prompt: "loop"})
	if err == nil {
		t.Fatalf("Run() error = nil")
	}
}

func TestAgentLoopDrainsSteerMailboxBeforeNextSampling(t *testing.T) {
	mailbox := NewSteerMailbox()
	agent := &fakeLoopAgent{enqueueSteerAfterFirstCall: true, steerMailbox: mailbox}
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("echo")}, func(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
		return &tool.Output{Success: true, Body: "tool result"}, nil
	})); err != nil {
		t.Fatalf("register echo: %v", err)
	}
	loop := NewAgentLoop(&AgentLoopOptions{
		Agent:        agent,
		Dispatcher:   NewToolDispatcher(&ToolDispatcherOptions{Router: tool.NewRouter(registry)}),
		SteerMailbox: mailbox,
		MaxTurns:     3,
	})

	result, err := loop.Run(context.Background(), &AgentLoopRequest{
		Prompt:       "run echo",
		ThreadID:     "thread-1",
		TurnID:       "turn-1",
		SteerMailbox: mailbox,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Response.Message != "done" {
		t.Fatalf("result = %#v", result)
	}
	if len(agent.requests) != 2 {
		t.Fatalf("requests = %#v", agent.requests)
	}
	found := false
	for _, raw := range agent.requests[1].InputItems {
		item, ok := raw.(map[string]any)
		if !ok || item["role"] != "user" {
			continue
		}
		if contentHasInputText(item["content"], "steered while tools run") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("second request input items = %#v", agent.requests[1].InputItems)
	}
}

func TestAgentLoopKeepsPromptWhenSteerExistsBeforeFirstSampling(t *testing.T) {
	mailbox := NewSteerMailbox()
	if err := mailbox.Enqueue(&SteerEnqueueParams{
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		InputItems: []any{map[string]any{
			"type": "message",
			"role": "user",
			"content": []map[string]any{{
				"type": "input_text",
				"text": "early steer",
			}},
		}},
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	agent := &fakeLoopAgent{}
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("echo")}, func(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
		return &tool.Output{Success: true, Body: "tool result"}, nil
	})); err != nil {
		t.Fatalf("register echo: %v", err)
	}
	loop := NewAgentLoop(&AgentLoopOptions{
		Agent:        agent,
		Dispatcher:   NewToolDispatcher(&ToolDispatcherOptions{Router: tool.NewRouter(registry)}),
		SteerMailbox: mailbox,
		MaxTurns:     3,
	})

	result, err := loop.Run(context.Background(), &AgentLoopRequest{
		Prompt:       "original prompt",
		ThreadID:     "thread-1",
		TurnID:       "turn-1",
		SteerMailbox: mailbox,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !resultInputItemsHaveText(result.InputItems, "original prompt") || !resultInputItemsHaveText(result.InputItems, "early steer") {
		t.Fatalf("result input items = %#v", result.InputItems)
	}
}

type fakeLoopAgent struct {
	requests                   []model.AgentRequest
	alwaysTool                 bool
	enqueueSteerAfterFirstCall bool
	steerMailbox               *SteerMailbox
}

type streamTimingLoopAgent struct {
	advance func(time.Duration)
}

type emptyInputLoopAgent struct {
	requests []model.AgentRequest
}

func (a *emptyInputLoopAgent) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	a.requests = append(a.requests, *request)
	return &model.AgentResponse{
		ResponseID: "resp-empty",
		Message:    "done",
		Items: []model.AgentItem{{
			ID:   "msg-empty",
			Type: "agent_message",
			Text: "done",
		}},
	}, nil
}

func (a *streamTimingLoopAgent) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	if a.advance != nil {
		a.advance(120 * time.Millisecond)
	}
	if request.StreamHandler != nil {
		request.StreamHandler(&model.ResponsesStreamEvent{
			Kind:  model.ResponsesStreamEventOutputText,
			Delta: "hello",
		})
	}
	if a.advance != nil {
		a.advance(300 * time.Millisecond)
	}
	return &model.AgentResponse{
		ResponseID: "resp-stream",
		Message:    "hello",
		Usage:      model.AgentUsage{InputTokens: 1, OutputTokens: 1},
		Items: []model.AgentItem{{
			ID:   "msg-stream",
			Type: "agent_message",
			Text: "hello",
		}},
	}, nil
}

func (a *fakeLoopAgent) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	a.requests = append(a.requests, *request)
	if a.enqueueSteerAfterFirstCall && a.steerMailbox != nil && len(a.requests) == 1 {
		_ = a.steerMailbox.Enqueue(&SteerEnqueueParams{
			ThreadID: request.ThreadID,
			TurnID:   request.TurnID,
			InputItems: []any{map[string]any{
				"type": "message",
				"role": "user",
				"content": []map[string]any{{
					"type": "input_text",
					"text": "steered while tools run",
				}},
			}},
		})
	}
	if a.alwaysTool || len(a.requests) == 1 {
		return &model.AgentResponse{
			ResponseID: "resp-tool",
			Usage:      model.AgentUsage{InputTokens: 5, CachedInputTokens: 1, OutputTokens: 3, ReasoningOutputTokens: 1},
			Items: []model.AgentItem{{
				ID:        "call-1",
				Type:      "function_call",
				Name:      "echo",
				CallID:    "call-1",
				Arguments: `{"text":"hello"}`,
			}},
		}, nil
	}
	return &model.AgentResponse{
		ResponseID: "resp-final",
		Message:    "done",
		Usage:      model.AgentUsage{InputTokens: 6, OutputTokens: 4, ReasoningOutputTokens: 1},
		Items: []model.AgentItem{{
			ID:   "msg-1",
			Type: "agent_message",
			Text: "done",
		}},
	}, nil
}

func contentHasInputText(raw any, want string) bool {
	content, ok := raw.([]map[string]any)
	if !ok {
		return false
	}
	for i := range content {
		if content[i]["type"] == "input_text" && content[i]["text"] == want {
			return true
		}
	}
	return false
}

func resultInputItemsHaveText(items []any, want string) bool {
	for _, raw := range items {
		switch item := raw.(type) {
		case map[string]any:
			if contentHasInputText(item["content"], want) {
				return true
			}
		}
	}
	return false
}
