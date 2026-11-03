package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

// finishSteerAgent answers each sampling attempt and records the payload so the
// test can prove the resampled turn saw the steer exactly once.
type finishSteerAgent struct {
	mu       sync.Mutex
	requests []string
}

func (a *finishSteerAgent) Run(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	payload := ""
	if request != nil {
		data, err := json.Marshal(request.InputItems)
		if err == nil {
			payload = string(data)
		}
	}
	a.mu.Lock()
	a.requests = append(a.requests, payload)
	index := len(a.requests)
	a.mu.Unlock()
	text := "first answer"
	if strings.Contains(payload, "steer-now") {
		text = "answer after steer"
	}
	return &model.AgentResponse{
		ResponseID: fmt.Sprintf("resp-%d", index),
		Message:    text,
		Items: []model.AgentItem{{
			ID:   fmt.Sprintf("msg-%d", index),
			Type: "message",
			Text: text,
		}},
	}, nil
}

func (a *finishSteerAgent) snapshot() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.requests...)
}

// Mirrors Rust #58877: a steer accepted before the turn closes input admission
// resumes sampling in the same turn, and the model sees it exactly once.
func TestTurnAcceptsSteerBeforeFinishingAndResamplesLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	agent := &finishSteerAgent{}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
		SteerMailbox: turn.NewSteerMailbox(),
	})
	router.SetNotificationSink(sink)

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir(), Prompt: "hello"}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID

	var hookOnce sync.Once
	router.turnFinishHook = func(hookThreadID string, hookTurnID string) {
		hookOnce.Do(func() {
			response := router.Handle(requestWithParams(t, IntID(3), MethodTurnSteer, turn.TurnSteerParams{
				ThreadID:       hookThreadID,
				ExpectedTurnID: hookTurnID,
				Prompt:         "steer-now",
			}))
			if response.Error != nil {
				t.Errorf("turn/steer during the finish window failed: %+v", response.Error)
			}
		})
	}

	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{ThreadID: threadID, Prompt: "hello"}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID

	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)
	record := waitForSessionItem(t, store, threadID, "agent_message")

	requests := agent.snapshot()
	if len(requests) != 2 {
		t.Fatalf("sampling attempts = %d, want 2 (the turn must resample): %#v", len(requests), requests)
	}
	if strings.Contains(requests[0], "steer-now") {
		t.Fatalf("the first attempt already saw the steer: %s", requests[0])
	}
	if count := strings.Count(requests[1], "steer-now"); count != 1 {
		t.Fatalf("the resampled request carried the steer %d times, want exactly once: %s", count, requests[1])
	}
	assistantText := ""
	assistantCount := 0
	for _, item := range record.Items {
		if item.Type == "agent_message" {
			assistantCount++
			assistantText = item.Text
		}
	}
	if assistantCount != 2 {
		t.Fatalf("agent messages = %d, want the first answer and the steer answer: %#v", assistantCount, record.Items)
	}
	if assistantText != "answer after steer" {
		t.Fatalf("final answer = %q, want the steer answer", assistantText)
	}
}
