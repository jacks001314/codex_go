package turn

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"codex_go/model"
	"codex_go/tool"
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

	var commentary []string
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
		OnAssistantMessage: func(response *model.AgentResponse, iteration int, hasToolCalls bool) {
			messages := ResponseAssistantMessages(response)
			text := ""
			if len(messages) > 0 {
				text = messages[0].Text
			}
			commentary = append(commentary, text+":"+strconv.Itoa(iteration)+":"+strconv.FormatBool(hasToolCalls))
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Response.Message != "done" || result.Iterations != 2 || len(result.ToolExecutions) != 1 {
		t.Fatalf("result = %#v", result)
	}
	if len(commentary) != 2 || commentary[0] != "I will run the echo command.:0:true" || commentary[1] != "done:1:false" {
		t.Fatalf("assistant callbacks = %#v", commentary)
	}
	if result.Usage.InputTokens != 11 || result.Usage.CachedInputTokens != 1 || result.Usage.OutputTokens != 7 || result.Usage.ReasoningOutputTokens != 2 || result.Usage.TotalTokens != 18 {
		t.Fatalf("usage = %#v", result.Usage)
	}
	if result.TimingProfile == nil || result.TimingProfile.SamplingRequestCount != 2 {
		t.Fatalf("timing profile = %#v", result.TimingProfile)
	}
	if len(agent.requests) != 2 || len(agent.requests[1].InputItems) != 4 {
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
	commentaryItem, ok := agent.requests[1].InputItems[1].(*model.AgentItem)
	if !ok || commentaryItem.Type != "agent_message" || commentaryItem.Text != "I will run the echo command." {
		t.Fatalf("commentary input = %#v", agent.requests[1].InputItems[1])
	}
	call, ok := agent.requests[1].InputItems[2].(*model.AgentItem)
	if !ok || call.Type != "function_call" || call.CallID != "call-1" {
		t.Fatalf("tool call input = %#v", agent.requests[1].InputItems[1])
	}
	item, ok := agent.requests[1].InputItems[3].(*ToolResponseItem)
	if !ok || item.Type != "function_call_output" || item.Output.Text() != "tool result" {
		t.Fatalf("tool output input = %#v", agent.requests[1].InputItems[2])
	}
}

func TestAgentLoopForwardsStreamEventsBeforeToolStarted(t *testing.T) {
	agent := &streamBeforeToolLoopAgent{}
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("echo")}, func(context.Context, *tool.Invocation) (*tool.Output, error) {
		return &tool.Output{Success: true, Body: "tool result"}, nil
	})); err != nil {
		t.Fatalf("register echo: %v", err)
	}
	runtime := NewRuntime(&RuntimeOptions{
		Agent:    agent,
		Router:   tool.NewRouter(registry),
		MaxTurns: 2,
	})

	var events []string
	_, err := runtime.Run(context.Background(), &AgentLoopRequest{
		Prompt: "run echo",
		StreamHandler: func(event *model.ResponsesStreamEvent) {
			if event != nil && event.Kind == model.ResponsesStreamEventOutputText {
				events = append(events, "commentary:"+event.Delta)
			}
		},
		OnToolStarted: func(context.Context, *tool.Invocation, time.Time) {
			events = append(events, "tool-started")
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := strings.Join(events, ","), "commentary:I will run echo.,tool-started"; got != want {
		t.Fatalf("event order = %q, want %q", got, want)
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

func TestAgentLoopSamplingFollowUpContinuesWithDeveloperInput(t *testing.T) {
	agent := &followUpLoopAgent{}
	loop := NewAgentLoop(&AgentLoopOptions{Agent: agent, MaxTurns: 3})
	called := 0
	result, err := loop.Run(context.Background(), &AgentLoopRequest{
		Prompt: "do work",
		SamplingFollowUp: func(ctx *SamplingFollowUpContext) []any {
			called++
			if called == 1 && ctx != nil && !ctx.HasToolCalls && model.AgentUsageTotalTokens(ctx.Usage) == 100 {
				return []any{model.DeveloperMessageInputItem("Save important state.")}
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Iterations != 2 || result.Usage.TotalTokens != 120 || result.SamplingFollowUps != 1 || len(agent.requests) != 2 {
		t.Fatalf("result = %+v requests=%d", result, len(agent.requests))
	}
	found := false
	for _, raw := range agent.requests[1].InputItems {
		item, ok := raw.(map[string]any)
		if ok && item["role"] == "developer" && contentHasInputText(item["content"], "Save important state.") {
			found = true
		}
	}
	if !found {
		t.Fatalf("second request input = %#v", agent.requests[1].InputItems)
	}
}

func TestAgentLoopSamplingFollowUpAfterToolKeepsToolOutput(t *testing.T) {
	agent := &fakeLoopAgent{}
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("echo")}, func(context.Context, *tool.Invocation) (*tool.Output, error) {
		return &tool.Output{Success: true, Body: "tool result"}, nil
	})); err != nil {
		t.Fatal(err)
	}
	loop := NewAgentLoop(&AgentLoopOptions{Agent: agent, Dispatcher: NewToolDispatcher(&ToolDispatcherOptions{Router: tool.NewRouter(registry)}), MaxTurns: 3})
	delivered := false
	_, err := loop.Run(context.Background(), &AgentLoopRequest{Prompt: "run", Tools: []any{"same-tools"}, SamplingFollowUp: func(ctx *SamplingFollowUpContext) []any {
		if !delivered && ctx != nil && ctx.HasToolCalls {
			delivered = true
			return []any{model.DeveloperMessageInputItem("Save state.")}
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(agent.requests) != 2 || len(agent.requests[0].Tools) != 1 || len(agent.requests[1].Tools) != 1 {
		t.Fatalf("requests = %#v", agent.requests)
	}
	if !resultInputItemsHaveText(agent.requests[1].InputItems, "Save state.") {
		t.Fatalf("second input = %#v", agent.requests[1].InputItems)
	}
	foundOutput := false
	for _, item := range agent.requests[1].InputItems {
		if output, ok := item.(*ToolResponseItem); ok && output.Output.Text() == "tool result" {
			foundOutput = true
		}
	}
	if !foundOutput {
		t.Fatalf("tool output missing: %#v", agent.requests[1].InputItems)
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

func TestAgentLoopDefaultAllowsLongToolChains(t *testing.T) {
	agent := &countingToolLoopAgent{toolRounds: 12}
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("echo")}, func(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
		return &tool.Output{Success: true, Body: "again"}, nil
	})); err != nil {
		t.Fatalf("register echo: %v", err)
	}
	loop := NewAgentLoop(&AgentLoopOptions{
		Agent:      agent,
		Dispatcher: NewToolDispatcher(&ToolDispatcherOptions{Router: tool.NewRouter(registry)}),
	})

	result, err := loop.Run(context.Background(), &AgentLoopRequest{Prompt: "loop a while"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Iterations != 13 || len(result.ToolExecutions) != 12 || result.Response.Message != "done" {
		t.Fatalf("result = %#v", result)
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
		ClientMetadata: map[string]string{
			"fiber_run_id": "fiber-start-123",
		},
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
	if agent.requests[0].ClientMetadata["fiber_run_id"] != "fiber-start-123" {
		t.Fatalf("first request client metadata = %#v", agent.requests[0].ClientMetadata)
	}
	if agent.requests[1].ClientMetadata["fiber_run_id"] != "fiber-steer-456" || agent.requests[1].ClientMetadata["origin"] != "gaas" {
		t.Fatalf("second request client metadata = %#v", agent.requests[1].ClientMetadata)
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

type countingToolLoopAgent struct {
	requests   []model.AgentRequest
	toolRounds int
}

type streamTimingLoopAgent struct {
	advance func(time.Duration)
}

type streamBeforeToolLoopAgent struct {
	requests int
}

type emptyInputLoopAgent struct {
	requests []model.AgentRequest
}

type followUpLoopAgent struct{ requests []model.AgentRequest }

func (a *followUpLoopAgent) Run(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	a.requests = append(a.requests, *request)
	usage := model.AgentUsage{TotalTokens: 100}
	message := "initial"
	if len(a.requests) > 1 {
		usage = model.AgentUsage{TotalTokens: 20}
		message = "done"
	}
	return &model.AgentResponse{ResponseID: "resp-" + strconv.Itoa(len(a.requests)), Message: message, Usage: usage, Items: []model.AgentItem{{Type: "agent_message", Text: message}}}, nil
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

func (a *streamBeforeToolLoopAgent) Run(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	a.requests++
	if a.requests == 1 {
		if request.StreamHandler != nil {
			request.StreamHandler(&model.ResponsesStreamEvent{
				Kind:   model.ResponsesStreamEventOutputAdded,
				ItemID: "commentary-1",
				Item: &model.AgentItem{
					ID:   "commentary-1",
					Type: "agent_message",
					Data: map[string]any{"phase": "commentary"},
				},
			})
			request.StreamHandler(&model.ResponsesStreamEvent{
				Kind:   model.ResponsesStreamEventOutputText,
				ItemID: "commentary-1",
				Delta:  "I will run echo.",
			})
		}
		return &model.AgentResponse{
			ResponseID: "resp-tool",
			Items: []model.AgentItem{
				{ID: "commentary-1", Type: "agent_message", Text: "I will run echo.", Data: map[string]any{"phase": "commentary"}},
				{ID: "call-1", Type: "function_call", Name: "echo", CallID: "call-1", Arguments: `{}`},
			},
		}, nil
	}
	return &model.AgentResponse{
		ResponseID: "resp-final",
		Message:    "done",
		Items:      []model.AgentItem{{ID: "final-1", Type: "agent_message", Text: "done", Data: map[string]any{"phase": "final_answer"}}},
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
			ClientMetadata: map[string]string{
				"fiber_run_id": "fiber-steer-456",
				"origin":       "gaas",
			},
		})
	}
	if a.alwaysTool || len(a.requests) == 1 {
		return &model.AgentResponse{
			ResponseID: "resp-tool",
			Usage:      model.AgentUsage{InputTokens: 5, CachedInputTokens: 1, OutputTokens: 3, ReasoningOutputTokens: 1},
			Items: []model.AgentItem{{ID: "commentary-1", Type: "agent_message", Text: "I will run the echo command."}, {
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

func (a *countingToolLoopAgent) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	a.requests = append(a.requests, *request)
	round := len(a.requests)
	if round <= a.toolRounds {
		callID := "call-" + strconv.Itoa(round)
		return &model.AgentResponse{
			ResponseID: "resp-tool-" + strconv.Itoa(round),
			Items: []model.AgentItem{{
				ID:        callID,
				Type:      "function_call",
				Name:      "echo",
				CallID:    callID,
				Arguments: `{"text":"hello"}`,
			}},
		}, nil
	}
	return &model.AgentResponse{
		ResponseID: "resp-final",
		Message:    "done",
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
