package appserver

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/config"
	"codex_go/session"
	"codex_go/turn"
)

// TestRuntimeRouterTurnReasoningSummaryResolvesConfigLikeRust covers Rust
// #43921's request-side behavior: the effective `model_reasoning_summary`
// config value reaches the model request, and an explicit turn summary
// overrides it.
func TestRuntimeRouterTurnReasoningSummaryResolvesConfigLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte("model_reasoning_summary = \"concise\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	store := session.NewStore(filepath.Join(home, "sessions"))
	sink := NewNotificationBuffer()
	agent := newRecordingRuntimeAgent("Done")
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
	})
	defer router.Close()
	router.SetNotificationSink(sink)

	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir()}))
	if start.Error != nil {
		t.Fatalf("thread start error: %+v", start.Error)
	}
	threadID := start.Result.(*ThreadStartResponse).Thread.ID

	first := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "hello",
	}))
	if first.Error != nil {
		t.Fatalf("turn start error: %+v", first.Error)
	}
	request := waitForRuntimeAgentRequest(t, agent)
	if request.ReasoningSummary != "concise" {
		t.Fatalf("config reasoning summary = %q, want concise", request.ReasoningSummary)
	}
	waitForTurnCompletedStatus(t, sink, first.Result.(*turn.TurnStartResponse).Turn.ID, TurnStatusCompleted)

	// An explicit turn summary wins over the config value.
	summary := "auto"
	second := router.Handle(requestWithParams(t, IntID(3), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "again",
		Summary:  &summary,
	}))
	if second.Error != nil {
		t.Fatalf("turn start error: %+v", second.Error)
	}
	request = waitForRuntimeAgentRequest(t, agent)
	if request.ReasoningSummary != "auto" {
		t.Fatalf("explicit reasoning summary = %q, want auto", request.ReasoningSummary)
	}
	waitForTurnCompletedStatus(t, sink, second.Result.(*turn.TurnStartResponse).Turn.ID, TurnStatusCompleted)
}
