
package appserver

import (
	"context"
	"testing"

	"codex_go/compact"
	"codex_go/config"
	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

// TestTurnEndCompactionThresholdReachedLikeRust mirrors Rust #46541's
// `turn_end_compaction_threshold_reached`: a zero percentage disables the
// feature, an existing auto-compaction limit counts as reached, and otherwise
// the configured percentage of the usable context window is required.
func TestTurnEndCompactionThresholdReachedLikeRust(t *testing.T) {
	cfg := &config.Config{Values: map[string]any{"model_post_turn_compact_threshold_percent": 50}}
	if !turnEndCompactionThresholdReached(cfg, &compact.TokenStatus{ActiveContextTokens: 5000}, 10000) {
		t.Fatal("5000/10000 tokens at 50% must reach the threshold")
	}
	if turnEndCompactionThresholdReached(cfg, &compact.TokenStatus{ActiveContextTokens: 4999}, 10000) {
		t.Fatal("4999/10000 tokens at 50% must not reach the threshold")
	}
	if turnEndCompactionThresholdReached(cfg, &compact.TokenStatus{ActiveContextTokens: 9000}, 0) {
		t.Fatal("an unknown window must not reach the percentage threshold")
	}
	if !turnEndCompactionThresholdReached(cfg, &compact.TokenStatus{ActiveContextTokens: 0, ShouldCompact: true}, 10000) {
		t.Fatal("an existing auto-compaction limit must reach the threshold")
	}
	if turnEndCompactionThresholdReached(&config.Config{}, &compact.TokenStatus{ActiveContextTokens: 9000}, 10000) {
		t.Fatal("an omitted percentage must disable turn-end compaction")
	}
}

type postTurnCompactRuntimeAgent struct {
	usage model.AgentUsage
}

func (a *postTurnCompactRuntimeAgent) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	return &model.AgentResponse{
		ResponseID: "resp-post-turn",
		Message:    "done",
		Items:      []model.AgentItem{{ID: "msg-post-turn", Type: "agent_message", Text: "done"}},
		Usage:      a.usage,
	}, nil
}

// TestRuntimeRouterPostTurnCompactLikeRust mirrors Rust #46541 end to end: a
// turn whose final response reaches the configured percentage of the usable
// context window compacts in the PostTurn phase before the turn completes.
func TestRuntimeRouterPostTurnCompactLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	runner := &recordingCompactRunner{summary: "post turn summary"}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter:  NewRouter(store),
		Turns:         turn.NewTurnService(),
		Agent:         &postTurnCompactRuntimeAgent{usage: model.AgentUsage{InputTokens: 5000, OutputTokens: 100}},
		ThreadStatus:  NewThreadStatusManager(),
		CompactRunner: runner,
	})
	router.SetNotificationSink(sink)

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:    t.TempDir(),
		Prompt: "seed",
	}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "finish the turn",
		Config: map[string]any{
			"model_post_turn_compact_threshold_percent": 50,
			"model_context_window":                      10000,
		},
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)

	if runner.request == nil {
		t.Fatal("post-turn compaction did not run")
	}
	if runner.request.Phase != compact.PhasePostTurn || runner.request.Trigger != compact.TriggerAuto {
		t.Fatalf("compaction request = %#v, want an auto PostTurn compaction", runner.request)
	}
	record, err := store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		t.Fatalf("Read thread error = %v", err)
	}
	if record.Metadata.Extra["compaction_phase"] != string(compact.PhasePostTurn) {
		t.Fatalf("compaction_phase = %#v, want postTurn", record.Metadata.Extra["compaction_phase"])
	}

	// Zero disables the feature even when the window would be exceeded.
	zeroRunner := &recordingCompactRunner{summary: "unused"}
	zeroRouter := NewRuntimeRouter(RuntimeServices{
		ThreadRouter:  NewRouter(session.NewStore(t.TempDir())),
		Turns:         turn.NewTurnService(),
		Agent:         &postTurnCompactRuntimeAgent{usage: model.AgentUsage{InputTokens: 5000, OutputTokens: 100}},
		ThreadStatus:  NewThreadStatusManager(),
		CompactRunner: zeroRunner,
	})
	zeroSink := NewNotificationBuffer()
	zeroRouter.SetNotificationSink(zeroSink)
	zeroStart := zeroRouter.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir(), Prompt: "seed"}))
	if zeroStart.Error != nil {
		t.Fatalf("thread start error: %+v", zeroStart.Error)
	}
	zeroThreadID := zeroStart.Result.(*ThreadStartResponse).Thread.ID
	zeroTurn := zeroRouter.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: zeroThreadID,
		Prompt:   "finish the turn",
		Config:   map[string]any{"model_post_turn_compact_threshold_percent": 0, "model_context_window": 10000},
	}))
	if zeroTurn.Error != nil {
		t.Fatalf("turn start error: %+v", zeroTurn.Error)
	}
	waitForTurnCompletedStatus(t, zeroSink, zeroTurn.Result.(*turn.TurnStartResponse).Turn.ID, TurnStatusCompleted)
	if zeroRunner.request != nil {
		t.Fatalf("zero percent compacted: %#v", zeroRunner.request)
	}
}
