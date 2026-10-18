package appserver

import (
	"testing"

	"codex_go/model"
	"codex_go/rollout"
	"codex_go/session"
	"codex_go/turn"
)

// Mirrors Rust #41912: each completed response leaves a durable usage record
// with response/turn/thread/root-turn attribution.
func TestRuntimeRouterPersistsTokenUsageRecordLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(home)
	sink := NewNotificationBuffer()
	usage := model.AgentUsage{InputTokens: 120, CachedInputTokens: 20, OutputTokens: 30, ReasoningOutputTokens: 7}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        &postTurnCompactRuntimeAgent{usage: usage},
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)

	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:    t.TempDir(),
		Prompt: "persist usage",
	}))
	if start.Error != nil {
		t.Fatalf("thread/start error = %+v", start.Error)
	}
	threadID := start.Result.(*ThreadStartResponse).Thread.ID
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "persist usage",
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn/start error = %+v", turnStart.Error)
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)

	record, err := store.Read(session.ThreadID(threadID), true, false)
	if err != nil {
		t.Fatalf("Read(thread) error = %v", err)
	}
	path, _ := record.Metadata.Extra["rollout_path"].(string)
	lines, _, err := rollout.Load(path)
	if err != nil {
		t.Fatalf("rollout.Load() error = %v", err)
	}
	records := rollout.TokenUsageRecords(lines)
	if len(records) != 1 {
		t.Fatalf("token usage records = %#v, want 1", records)
	}
	got := records[0]
	if got.ThreadID != threadID || got.SessionID != threadID || got.TurnID != turnID || got.RootTurnID != turnID {
		t.Fatalf("usage attribution = %#v", got)
	}
	if got.ResponseID != "resp-post-turn" {
		t.Fatalf("response id = %q, want resp-post-turn", got.ResponseID)
	}
	if got.Usage["total_tokens"] != float64(150) || got.TurnTokenUsage["total_tokens"] != float64(150) {
		t.Fatalf("usage totals = %#v / %#v, want 150", got.Usage, got.TurnTokenUsage)
	}
	if total, ok := got.ThreadTokenUsage["total_tokens"].(float64); !ok || total < 150 {
		t.Fatalf("thread total = %#v, want at least 150", got.ThreadTokenUsage)
	}
}
