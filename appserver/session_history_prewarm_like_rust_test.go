package appserver

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/model"
	"codex_go/session"
)

// socketResumePrewarmAgent models the cached WebSocket session that Rust #47709
// routes resume prewarms through: a warmup on a warm socket skips preparation,
// while a closed socket produces a freshly prepared response.
type socketResumePrewarmAgent struct {
	*prewarmingRuntimeAgent
	mu       sync.Mutex
	warm     bool
	calls    int
	prepared int
}

func (a *socketResumePrewarmAgent) Prewarm(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
	if a.warm {
		return nil, nil
	}
	a.warm = true
	a.prepared++
	return &model.AgentResponse{ResponseID: fmt.Sprintf("warm-%d", a.prepared)}, nil
}

func (a *socketResumePrewarmAgent) stats() (int, int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls, a.prepared
}

func (a *socketResumePrewarmAgent) closeSocket() {
	a.mu.Lock()
	a.warm = false
	a.mu.Unlock()
}

// TestIdleHistoryPrewarmRechecksCachedSocketLikeRust pins Rust #47709: a
// completed resume warmup is returned to the cache and the next resume re-checks
// the socket instead of reusing the previous outcome blindly. A warm skip keeps
// the prepared baseline; a closed socket schedules a new prepared response.
func TestIdleHistoryPrewarmRechecksCachedSocketLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	record := &session.Record{
		ID: "child", SessionID: "child", ParentThreadID: "root",
		CreatedAt: now, UpdatedAt: now,
		Metadata: session.Metadata{CWD: home, Model: "gpt-test", AgentPath: "/root/child", MultiAgentVersion: "v2"},
		Items: []session.Item{
			{ID: "user-1", Type: "message", Role: "user", Text: "first task", CreatedAt: now},
			{ID: "assistant-1", Type: "message", Role: "assistant", Text: "first result", CreatedAt: now},
		},
	}
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	agent := &socketResumePrewarmAgent{prewarmingRuntimeAgent: &prewarmingRuntimeAgent{
		recordingRuntimeAgent: newRecordingRuntimeAgent("ok"),
	}}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
	})
	t.Cleanup(func() { _ = router.Close() })
	retainIdleLifecycleThread(t, router, "child")

	waitForPrewarm := func(calls int) *startupPrewarmState {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for {
			got, _ := agent.stats()
			state := router.startupPrewarmsSnapshot()["child"]
			if got >= calls && state != nil && state.finished {
				return state
			}
			if time.Now().After(deadline) {
				t.Fatalf("history prewarm %d did not finish (calls=%d, state=%#v)", calls, got, state)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}

	router.scheduleHistoryPrewarm("child")
	if state := waitForPrewarm(1); state.responseID != "warm-1" {
		t.Fatalf("first prewarm response id = %q, want warm-1", state.responseID)
	}

	// The completed warmup no longer short-circuits the next resume: the socket
	// is re-checked and a warm skip keeps the prepared baseline.
	router.scheduleHistoryPrewarm("child")
	state := waitForPrewarm(2)
	if state.responseID != "warm-1" {
		t.Fatalf("warm skip response id = %q, want the retained warm-1", state.responseID)
	}

	// The peer closed the socket, so the next resume prepares a new response.
	agent.closeSocket()
	router.scheduleHistoryPrewarm("child")
	state = waitForPrewarm(3)
	if state.responseID != "warm-2" {
		t.Fatalf("repaired prewarm response id = %q, want warm-2", state.responseID)
	}
}

type historyPrewarmAgent struct {
	*prewarmingRuntimeAgent
	mu       sync.Mutex
	requests []*model.AgentRequest
}

func (a *historyPrewarmAgent) Prewarm(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	a.mu.Lock()
	a.requests = append(a.requests, request)
	a.mu.Unlock()
	return a.prewarmingRuntimeAgent.Prewarm(ctx, request)
}

func (a *historyPrewarmAgent) lastRequest() *model.AgentRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.requests) == 0 {
		return nil
	}
	return a.requests[len(a.requests)-1]
}

func TestIdleHistoryPrewarmReusesOnlyOnPrefixAndSettingsLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	record := &session.Record{
		ID: "child", SessionID: "child", ParentThreadID: "root",
		CreatedAt: now, UpdatedAt: now,
		Metadata: session.Metadata{CWD: home, Model: "gpt-test", AgentPath: "/root/child", MultiAgentVersion: "v2"},
		Items: []session.Item{
			{ID: "user-1", Type: "message", Role: "user", Text: "first task", CreatedAt: now},
			{ID: "assistant-1", Type: "message", Role: "assistant", Text: "first result", CreatedAt: now},
		},
	}
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	agent := &historyPrewarmAgent{prewarmingRuntimeAgent: &prewarmingRuntimeAgent{
		recordingRuntimeAgent: newRecordingRuntimeAgent("ok"),
		prewarmedID:           "history-prewarm-resp",
	}}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
	})
	t.Cleanup(func() { _ = router.Close() })
	retainIdleLifecycleThread(t, router, "child")

	waitForHistoryPrewarm := func(previous int) *model.AgentRequest {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for {
			agent.mu.Lock()
			count := len(agent.requests)
			agent.mu.Unlock()
			if count > previous {
				return agent.lastRequest()
			}
			if time.Now().After(deadline) {
				t.Fatal("history prewarm did not run")
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	waitForPrewarmFinished := func() {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for {
			if state := router.startupPrewarmsSnapshot()["child"]; state != nil && state.finished {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("history prewarm did not finish")
			}
			time.Sleep(5 * time.Millisecond)
		}
	}

	router.scheduleHistoryPrewarm("child")
	request := waitForHistoryPrewarm(0)
	waitForPrewarmFinished()
	if request == nil || len(request.InputItems) == 0 || request.Originator != "session_startup" {
		t.Fatalf("history prewarm request = %#v", request)
	}
	state := router.startupPrewarmsSnapshot()["child"]
	if state == nil || state.inputMode != "history" || !state.finished {
		t.Fatalf("history prewarm state = %#v", state)
	}
	settingsKey := prewarmSettingsKey(request.Model, request.Instructions, "")
	extended := append(append([]any(nil), request.InputItems...), map[string]any{
		"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "next task"}},
	})
	if id, consumed := router.takeStartupPrewarmResponseID("child", extended, settingsKey); id != "history-prewarm-resp" || consumed != len(request.InputItems) {
		t.Fatalf("prefix reuse id = %q consumed = %d", id, consumed)
	}

	// A non-prefix prompt must not chain the prepared response.
	router.scheduleHistoryPrewarm("child")
	request = waitForHistoryPrewarm(1)
	waitForPrewarmFinished()
	nonPrefix := append([]any{map[string]any{"type": "message", "role": "user", "content": "different"}}, request.InputItems...)
	if id, _ := router.takeStartupPrewarmResponseID("child", nonPrefix, settingsKey); id != "" {
		t.Fatalf("non-prefix reuse id = %q", id)
	}

	// Changed model settings must not chain the prepared response either.
	router.scheduleHistoryPrewarm("child")
	request = waitForHistoryPrewarm(2)
	waitForPrewarmFinished()
	if id, _ := router.takeStartupPrewarmResponseID("child", request.InputItems, prewarmSettingsKey("other-model", request.Instructions, "")); id != "" {
		t.Fatalf("settings-mismatch reuse id = %q", id)
	}
}
