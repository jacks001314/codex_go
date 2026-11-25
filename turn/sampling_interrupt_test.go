package turn

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"codex_go/model"
	"codex_go/tool"
)

type boundaryTestRunner func(context.Context, *model.AgentRequest) (*model.AgentResponse, error)

func (f boundaryTestRunner) Run(ctx context.Context, req *model.AgentRequest) (*model.AgentResponse, error) {
	return f(ctx, req)
}

func TestSamplingInterruptGate(t *testing.T) {
	for _, tc := range []struct {
		on              bool
		version, parent string
		want            bool
	}{
		{false, "v2", "parent", false}, {true, "v1", "parent", false},
		{true, "v2", "", false}, {true, "disabled", "parent", false}, {true, "v2", "parent", true},
	} {
		if got := SamplingInterruptEnabled(tc.on, tc.version, tc.parent); got != tc.want {
			t.Fatalf("%+v: %v", tc, got)
		}
	}
}

func TestSamplingInterruptRegistrationAndTerminal(t *testing.T) {
	s := NewSamplingInterrupt("turn")
	if _, err := s.Register(context.Background(), "wrong", nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Register(cancelled, "turn", nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	tools, err := s.Register(context.Background(), "turn", nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.Unregister() {
		t.Fatal("normal unregister reported pending stop")
	}
	if tools.Err() == nil {
		t.Fatal("attempt resources not released")
	}
	tools, err = s.Register(context.Background(), "turn", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Request() || tools.Err() == nil {
		t.Fatal("tools were not immediately cancelled")
	}
	if !s.Unregister() || !s.Request() {
		t.Fatal("pending stop lost")
	}
	select {
	case <-s.Finished():
		t.Fatal("finished before terminal")
	default:
	}
	if _, err := s.Register(context.Background(), "turn", nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	s.Finish()
	s.Finish()
	select {
	case <-s.Finished():
	default:
		t.Fatal("terminal not released")
	}
	fallback := NewSamplingInterrupt("turn")
	if fallback.Request() {
		t.Fatal("unregistered request must hard-abort")
	}
	if _, err := fallback.Register(context.Background(), "turn", nil); !errors.Is(err, context.Canceled) {
		t.Fatal("abort race admitted a sampler")
	}
}

func TestSamplingInterruptRetainsCurrentItem(t *testing.T) {
	for _, kind := range []string{"message", "function_call", "custom_tool_call", "tool_search_call"} {
		t.Run(kind, func(t *testing.T) {
			stop := NewSamplingInterrupt("turn")
			kept := model.AgentItem{ID: "keep", Type: kind, Text: "complete current answer", Name: "echo", CallID: "kept-call", Arguments: "{}", Input: "hello"}
			later := model.AgentItem{ID: "drop", Type: "message", Text: "discard me"}
			var forwarded []string
			runner := boundaryTestRunner(func(ctx context.Context, req *model.AgentRequest) (*model.AgentResponse, error) {
				req.StreamHandler(&model.ResponsesStreamEvent{Kind: model.ResponsesStreamEventOutputAdded, Item: &kept})
				if !stop.Request() {
					t.Fatal("no active sampling registration")
				}
				if ctx.Err() != nil {
					t.Fatal("current item cancelled before done")
				}
				req.StreamHandler(&model.ResponsesStreamEvent{Kind: model.ResponsesStreamEventOutputText, Delta: "complete current answer"})
				req.StreamHandler(&model.ResponsesStreamEvent{Kind: model.ResponsesStreamEventOutputDone, Item: &kept})
				if ctx.Err() == nil {
					t.Fatal("sampler not cancelled at boundary")
				}
				// A buffered transport callback after cancellation must not leak.
				req.StreamHandler(&model.ResponsesStreamEvent{Kind: model.ResponsesStreamEventOutputAdded, Item: &later})
				req.StreamHandler(&model.ResponsesStreamEvent{Kind: model.ResponsesStreamEventOutputDone, Item: &later})
				return nil, ctx.Err()
			})
			result, err := NewAgentLoop(&AgentLoopOptions{Agent: runner}).Run(context.Background(), &AgentLoopRequest{
				TurnID: "turn", Prompt: "question", SamplingInterrupt: stop,
				StreamHandler: func(event *model.ResponsesStreamEvent) {
					if event.Item != nil {
						forwarded = append(forwarded, event.Item.ID)
					}
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if !result.SamplingInterrupted || len(result.Response.Items) != 1 || result.Response.Items[0].ID != "keep" {
				t.Fatalf("%+v", result)
			}
			for _, id := range forwarded {
				if id == "drop" {
					t.Fatal("later output leaked")
				}
			}
			data, _ := json.Marshal(result.InputItems)
			if strings.Contains(string(data), "discard me") {
				t.Fatalf("discarded history: %s", data)
			}
			if kind != "message" && kind != "tool_search_call" && (!strings.Contains(string(data), "kept-call") || !strings.Contains(string(data), "aborted")) {
				t.Fatalf("missing aborted pair: %s", data)
			}
			outputKind := map[string]string{"function_call": "function_call_output", "custom_tool_call": "custom_tool_call_output", "tool_search_call": "tool_search_output"}[kind]
			if outputKind != "" && !strings.Contains(string(data), outputKind) {
				t.Fatalf("incorrect aborted output shape: %s", data)
			}
		})
	}
}

func TestSamplingInterruptBeforeFirstAndBetweenItems(t *testing.T) {
	for _, beforeFirst := range []bool{true, false} {
		stop := NewSamplingInterrupt("turn")
		runner := boundaryTestRunner(func(ctx context.Context, req *model.AgentRequest) (*model.AgentResponse, error) {
			item := model.AgentItem{ID: "first", Type: "message", Text: "first"}
			if beforeFirst {
				if !stop.Request() {
					t.Fatal("request failed")
				}
				if ctx.Err() != nil {
					t.Fatal("first item must finish")
				}
			}
			req.StreamHandler(&model.ResponsesStreamEvent{Kind: model.ResponsesStreamEventOutputAdded, Item: &item})
			req.StreamHandler(&model.ResponsesStreamEvent{Kind: model.ResponsesStreamEventOutputDone, Item: &item})
			if !beforeFirst {
				if !stop.Request() {
					t.Fatal("request failed")
				}
			}
			if ctx.Err() == nil {
				t.Fatal("idle between items must cancel without another event")
			}
			return nil, ctx.Err()
		})
		result, err := NewAgentLoop(&AgentLoopOptions{Agent: runner}).Run(context.Background(), &AgentLoopRequest{TurnID: "turn", SamplingInterrupt: stop})
		if err != nil || len(result.Response.Items) != 1 {
			t.Fatalf("%+v / %v", result, err)
		}
	}
}

func TestSamplingInterruptCancelsRunningTool(t *testing.T) {
	stop := NewSamplingInterrupt("turn")
	started := make(chan struct{})
	var calls atomic.Int32
	runner := boundaryTestRunner(func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
		calls.Add(1)
		return &model.AgentResponse{Items: []model.AgentItem{{Type: "function_call", Name: "block", CallID: "call", Arguments: "{}"}}}, nil
	})
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("block")}, func(ctx context.Context, _ *tool.Invocation) (*tool.Output, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})); err != nil {
		t.Fatal(err)
	}
	done := make(chan *AgentLoopResult, 1)
	go func() {
		result, err := NewAgentLoop(&AgentLoopOptions{Agent: runner, Dispatcher: NewToolDispatcher(&ToolDispatcherOptions{Router: tool.NewRouter(registry)})}).Run(context.Background(), &AgentLoopRequest{TurnID: "turn", SamplingInterrupt: stop})
		if err != nil {
			done <- nil
			return
		}
		done <- result
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("tool never started")
	}
	if !stop.Request() {
		t.Fatal("request failed")
	}
	select {
	case result := <-done:
		if result == nil || !result.SamplingInterrupted || calls.Load() != 1 {
			t.Fatalf("%+v", result)
		}
		data, _ := json.Marshal(result.InputItems)
		if !strings.Contains(string(data), "aborted") {
			t.Fatalf("missing aborted output: %s", data)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("tool cancellation stalled")
	}
}

func TestSamplingInterruptCompactionInstallsBeforeStopping(t *testing.T) {
	stop := NewSamplingInterrupt("turn")
	runner := boundaryTestRunner(func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
		return &model.AgentResponse{Items: []model.AgentItem{{Type: "function_call", Name: "echo", CallID: "call", Arguments: "{}"}}}, nil
	})
	installed := false
	result, err := NewAgentLoop(&AgentLoopOptions{Agent: runner}).Run(context.Background(), &AgentLoopRequest{TurnID: "turn", SamplingInterrupt: stop, SamplingCompaction: func(c *SamplingCompactionContext) (*SamplingCompactionResult, error) {
		tools, err := stop.Register(c.Context, "turn", nil)
		if err != nil {
			return nil, err
		}
		if !stop.Request() || tools.Err() == nil || c.Context.Err() != nil {
			t.Fatal("summary transport incorrectly cancelled")
		}
		installed = true
		if !stop.Unregister() {
			t.Fatal("lost pending compaction stop")
		}
		return &SamplingCompactionResult{Compacted: true, InputItems: []any{map[string]any{"type": "compaction", "summary": "installed checkpoint"}}}, nil
	}})
	if err != nil || !installed || !result.SamplingInterrupted || result.Compactions != 1 {
		t.Fatalf("%+v / %v", result, err)
	}
	data, _ := json.Marshal(result.InputItems)
	if !strings.Contains(string(data), "installed checkpoint") || strings.Contains(string(data), "\"call\"") {
		t.Fatalf("history: %s", data)
	}
}

func TestSamplingInterruptFollowupsWaitForTerminalInSeparateTurns(t *testing.T) {
	stop := NewSamplingInterrupt("old")
	_, _ = stop.Register(context.Background(), "old", nil)
	stop.Request()
	started := make(chan int, 2)
	firstDone := make(chan struct{})
	secondDone := make(chan struct{})
	stop.EnqueueFollowup(func() { started <- 1; <-firstDone })
	stop.EnqueueFollowup(func() { started <- 2; close(secondDone) })
	select {
	case <-started:
		t.Fatal("followup ran before terminal")
	default:
	}
	stop.Finish()
	select {
	case n := <-started:
		if n != 1 {
			t.Fatal(n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first followup stalled")
	}
	select {
	case <-started:
		t.Fatal("second followup shared first turn")
	default:
	}
	close(firstDone)
	select {
	case <-secondDone:
	case <-time.After(5 * time.Second):
		t.Fatal("second followup stalled")
	}
}

func TestSamplingInterruptRuntimeWithoutRouterPreservesRequestAndMessage(t *testing.T) {
	stop := NewSamplingInterrupt("turn")
	hosted := map[string]any{"type": "web_search"}
	runner := boundaryTestRunner(func(ctx context.Context, req *model.AgentRequest) (*model.AgentResponse, error) {
		if len(req.Tools) != 1 || !req.UnifiedImageBudget || !req.SafeImageDecoder || !req.IncludeUsageAttribution {
			t.Fatalf("lost runtime request settings: %+v", req)
		}
		if !stop.Request() || ctx.Err() != nil {
			t.Fatal("message-only sampler canceled prematurely")
		}
		return &model.AgentResponse{Message: "retained message-only response"}, nil
	})
	runtime := NewRuntime(&RuntimeOptions{Agent: runner, HostedTools: []any{hosted}, UnifiedImageBudget: true, SafeImageDecoder: true, IncludeUsageAttribution: true})
	result, err := runtime.Run(context.Background(), &AgentLoopRequest{TurnID: "turn", SamplingInterrupt: stop})
	if err != nil || !result.SamplingInterrupted || result.Response.Message != "retained message-only response" {
		t.Fatalf("lost response: %+v / %v", result, err)
	}
	data, _ := json.Marshal(result.InputItems)
	if !strings.Contains(string(data), "retained message-only response") {
		t.Fatalf("message missing from history: %s", data)
	}
}
