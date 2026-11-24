package appserver

import (
	"context"
	"sync"
	"testing"

	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

// promptRecordOrderingRuntimeAgent snapshots the persisted thread during its
// first sampling request.
type promptRecordOrderingRuntimeAgent struct {
	store    *session.Store
	threadID string

	mu      sync.Mutex
	calls   int
	sampled []session.Item
	err     error
}

func (a *promptRecordOrderingRuntimeAgent) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	a.mu.Lock()
	a.calls++
	first := a.calls == 1
	a.mu.Unlock()
	if first {
		record, err := a.store.Read(session.ThreadID(a.threadID), true, true)
		a.mu.Lock()
		if err != nil {
			a.err = err
		} else if record != nil {
			a.sampled = append([]session.Item(nil), record.Items...)
		}
		a.mu.Unlock()
	}
	return &model.AgentResponse{
		ResponseID: "resp-prompt-order",
		Message:    "done",
		Items:      []model.AgentItem{{ID: "prompt-order-message", Type: "agent_message", Text: "done"}},
		Usage:      model.AgentUsage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
	}, nil
}

func (a *promptRecordOrderingRuntimeAgent) snapshot() ([]session.Item, int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]session.Item(nil), a.sampled...), a.calls, a.err
}

// Mirrors Rust #63523: the accepted prompt enters the conversation history
// before the turn reaches the model, so a failure during startup cannot drop
// it, and the completion path does not append a second copy.
func TestRuntimeRouterPersistsAcceptedPromptBeforeSamplingLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	cwd := t.TempDir()
	agent := &promptRecordOrderingRuntimeAgent{store: store}
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		ThreadStatus: NewThreadStatusManager(),
		Agent:        agent,
		DefaultCWD:   cwd,
	})
	router.SetNotificationSink(sink)
	defer router.Close()

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: cwd}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	// The next Handle call starts the turn worker, so this assignment happens
	// before the agent can observe the thread.
	agent.threadID = threadID

	const prompt = "accepted prompt is recorded before sampling"
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   prompt,
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)

	sampled, calls, err := agent.snapshot()
	if err != nil {
		t.Fatalf("reading the thread during sampling: %v", err)
	}
	if calls == 0 {
		t.Fatalf("agent never sampled the turn")
	}
	sampledPromptCount := 0
	for _, item := range sampled {
		if item.Text == prompt {
			sampledPromptCount++
		}
	}
	if sampledPromptCount != 1 {
		t.Fatalf("accepted prompt recorded %d times before sampling, want 1: %#v", sampledPromptCount, sampled)
	}

	record, err := store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		t.Fatalf("Read thread error = %v", err)
	}
	promptCount := 0
	promptIndex := -1
	agentIndex := -1
	for index, item := range record.Items {
		if item.Text == prompt {
			promptCount++
			if promptIndex < 0 {
				promptIndex = index
			}
		}
		if item.Type == "agent_message" && agentIndex < 0 {
			agentIndex = index
		}
	}
	if promptCount != 1 {
		t.Fatalf("accepted prompt persisted %d times, want 1: %#v", promptCount, record.Items)
	}
	if agentIndex >= 0 && promptIndex > agentIndex {
		t.Fatalf("accepted prompt followed model output in the final history: %#v", record.Items)
	}
}
