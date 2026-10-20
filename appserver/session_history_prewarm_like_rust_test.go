package appserver

import (
	"context"
	"sync"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/model"
	"codex_go/session"
)

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
