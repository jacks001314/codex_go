package appserver

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"codex_go/codexapi"
	"codex_go/compact"
	"codex_go/model"
	"codex_go/session"
	"codex_go/state"
	"codex_go/turn"
)

// usageLimitedCompactRunner fails every remote compaction attempt with the
// provider's usage-limit error (Rust `CodexErrorInfo::UsageLimitExceeded`).
type usageLimitedCompactRunner struct{ calls int }

func (r *usageLimitedCompactRunner) Compact(ctx context.Context, request *compact.Request) (*compact.Result, error) {
	r.calls++
	return nil, &codexapi.APIError{
		Kind:    codexapi.ErrorQuotaExceeded,
		Status:  http.StatusTooManyRequests,
		Message: "usage limit reached",
	}
}

// Mirrors Rust #49097 (codex-rs/core/src/session/turn.rs): a usage-limit
// failure during post-turn compaction notifies the turn lifecycle so the
// active goal stops as usage-limited, while the completed turn - and its final
// answer - is preserved instead of being failed.
func TestRuntimeRouterPostTurnCompactUsageLimitStopsGoalLikeRust(t *testing.T) {
	home := t.TempDir()
	ctx := context.Background()
	stateCfg, err := state.NewSqliteConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	stateRuntime, err := state.InitStateRuntime(ctx, stateCfg, "openai")
	if err != nil {
		t.Fatal(err)
	}
	defer stateRuntime.Close()
	store := session.NewStore(home)
	threadRouter := NewRouter(store)
	threadRouter.SetStateRuntime(stateRuntime)
	runner := &usageLimitedCompactRunner{}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter:  threadRouter,
		StateRuntime:  stateRuntime,
		Turns:         turn.NewTurnService(),
		Agent:         &postTurnCompactRuntimeAgent{usage: model.AgentUsage{InputTokens: 5000, OutputTokens: 100}},
		ThreadStatus:  NewThreadStatusManager(),
		CompactRunner: runner,
	})
	sink := NewNotificationBuffer()
	router.SetNotificationSink(sink)

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: home, Prompt: "seed"}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	base := time.Now().UTC()
	budget := int64(1_000_000)
	if err := stateRuntime.ReplaceThreadGoalSnapshot(ctx, &state.ThreadGoal{
		ThreadID: threadID, GoalID: "post-turn-usage-limit-goal",
		Objective: "stop on post-turn compaction usage limit",
		Status:    state.ThreadGoalActive, TokenBudget: &budget, CreatedAt: base, UpdatedAt: base,
	}); err != nil {
		t.Fatal(err)
	}

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
	// #49097 keeps the completed turn: the final answer still completes.
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)
	if runner.calls == 0 {
		t.Fatal("post-turn compaction never reached the remote runner")
	}
	warned := false
	for _, notification := range sink.List() {
		if notification.Method != NotificationWarning {
			continue
		}
		if warning, ok := notification.Params.(*WarningNotification); ok &&
			strings.HasPrefix(warning.Message, "Post-turn context compaction failed:") {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("post-turn compaction failure did not warn after the completed turn: %#v", sink.List())
	}
	goal, err := stateRuntime.GetThreadGoal(ctx, threadID)
	if err != nil {
		t.Fatal(err)
	}
	if goal == nil || goal.Status != state.ThreadGoalUsageLimited {
		t.Fatalf("goal after post-turn compaction usage limit = %#v, want %s", goal, state.ThreadGoalUsageLimited)
	}
}
