package appserver

import (
	"context"
	"errors"
	"testing"

	"codex_go/compact"
	"codex_go/config"
	"codex_go/model"
	"codex_go/runtimeutil"
	"codex_go/session"
)

// Rust #53670 ("Account for token usage from incomplete responses") also covers
// compaction: `core/src/compact_remote_v2.rs::collect_compaction_output` inspects
// a terminal `response.incomplete` event before propagating it and splits the
// accounting by request kind.
//
//   - Background (Rust `CompactionRequestKind::Background`, the auto compaction
//     that runs inline with a turn) may only charge the shared rollout budget:
//     `sess.record_rollout_budget_usage(usage)`. It must not publish foreground
//     token usage or move the context baseline (the upstream regression
//     `collect_background_incomplete_does_not_publish_foreground_usage` asserts
//     `token_usage_info` stays None).
//   - Foreground (a standalone/manual compaction) records the usage as
//     spending-only and emits the deferred token-count event.
//
// Neither arm may replace the terminal error, even when charging exhausts the
// budget (Rust ignores the result with `let _ = ...`).
type incompleteCompactionRunner struct {
	usage *model.AgentUsage
	err   error
}

func (r *incompleteCompactionRunner) Compact(context.Context, *compact.Request) (*compact.Result, error) {
	if r.err != nil {
		return nil, r.err
	}
	return nil, model.ResponseIncompleteError{
		Reason:     "max_output_tokens",
		ResponseID: "resp-compaction-incomplete",
		Usage:      r.usage,
	}
}

func compactionAccountingThread(t *testing.T, threadID string) (*RuntimeRouter, *session.Store) {
	t.Helper()
	home := t.TempDir()
	store := session.NewStore(t.TempDir())
	now := fixedTime()
	baseline := map[string]any{"input_tokens": int64(1000), "output_tokens": int64(100), "total_tokens": int64(1100)}
	if err := store.Create(&session.Record{
		ID: session.ThreadID(threadID), SessionID: threadID,
		CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{Model: "gpt-test", CWD: home, Extra: map[string]any{
			"total_token_usage":                           baseline,
			"last_token_usage":                            baseline,
			"model_context_window":                        int64(200000),
			"auto_compact_window_prefill":                 int64(1000),
			"auto_compact_window_prefill_server_observed": true,
			"token_usage_info": map[string]any{
				"total_token_usage":    baseline,
				"last_token_usage":     baseline,
				"model_context_window": int64(200000),
			},
		}},
		Items: []session.Item{
			{ID: "u1", Type: "message", Role: "user", Text: "first", CreatedAt: now},
			{ID: "a1", Type: "agent_message", Role: "assistant", Text: "answer", CreatedAt: now},
		},
	}); err != nil {
		t.Fatalf("Create record error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
	})
	return router, store
}

func runIncompleteCompaction(t *testing.T, router *RuntimeRouter, runner compact.RemoteRunner, request *runtimeCompactRequest) error {
	t.Helper()
	router.services.CompactRunner = runner
	_, err := router.compactThread(context.Background(), request)
	if err == nil {
		t.Fatal("compactThread() error = nil, want the terminal incomplete-response error")
	}
	if !model.IsResponseIncompleteError(err) {
		t.Fatalf("compactThread() error = %#v, want the terminal incomplete-response error", err)
	}
	if errors.Is(err, ErrSessionBudgetExceeded) {
		t.Fatalf("compactThread() error = %v, want the original terminal error", err)
	}
	return err
}

func TestForegroundIncompleteCompactionSpendsWithoutReplacingTheBaselineLikeRust(t *testing.T) {
	const threadID = "thread-incomplete-compaction-foreground"
	router, store := compactionAccountingThread(t, threadID)
	budget := runtimeutil.NewBudget()
	budget.Configure(runtimeutil.BudgetConfig{LimitTokens: 1})
	router.rolloutBudgetOnce.Do(func() {})
	router.rolloutBudget = budget

	usage := model.AgentUsage{InputTokens: 120, OutputTokens: 30}
	runIncompleteCompaction(t, router, &incompleteCompactionRunner{usage: &usage}, &runtimeCompactRequest{
		ThreadID:   threadID,
		TurnID:     "turn-compact-foreground",
		Trigger:    compact.TriggerManual,
		Reason:     compact.ReasonUserRequested,
		Phase:      compact.PhaseStandaloneTurn,
		RemoteOnly: true,
	})

	record, err := store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		t.Fatalf("Read record error = %v", err)
	}
	extra := record.Metadata.Extra
	total := tokenUsageBreakdownFromMetadata(extra["total_token_usage"])
	if total == nil || total.TotalTokens != 1100+150 {
		t.Fatalf("total_token_usage = %#v, want %d", extra["total_token_usage"], 1100+150)
	}
	if last := tokenUsageBreakdownFromMetadata(extra["last_token_usage"]); last == nil || last.TotalTokens != 1100 {
		t.Fatalf("last_token_usage = %#v, want the untouched baseline (1100)", extra["last_token_usage"])
	}
	if got := intFromAny(extra["auto_compact_window_prefill"]); got != 1000 {
		t.Fatalf("auto_compact_window_prefill = %d, want the untouched baseline (1000)", got)
	}
	info, ok := extra["token_usage_info"].(map[string]any)
	if !ok {
		t.Fatalf("token_usage_info = %#v", extra["token_usage_info"])
	}
	infoTotal := tokenUsageBreakdownFromMetadata(firstMapValue(info, "total_token_usage", "totalTokenUsage", "total"))
	if infoTotal == nil || infoTotal.TotalTokens != 1100+150 {
		t.Fatalf("token_usage_info.total_token_usage = %#v, want %d", info["total_token_usage"], 1100+150)
	}
	infoLast := tokenUsageBreakdownFromMetadata(firstMapValue(info, "last_token_usage", "lastTokenUsage", "last"))
	if infoLast == nil || infoLast.TotalTokens != 1100 {
		t.Fatalf("token_usage_info.last_token_usage = %#v, want the untouched baseline", info["last_token_usage"])
	}
	if !router.rolloutBudgetCharged.Load() || !router.rolloutBudgetExhausted.Load() {
		t.Fatalf("budget charged=%v exhausted=%v, want the foreground usage charged and the exhaustion recorded",
			router.rolloutBudgetCharged.Load(), router.rolloutBudgetExhausted.Load())
	}
}

func TestBackgroundIncompleteCompactionOnlyChargesTheBudgetLikeRust(t *testing.T) {
	const threadID = "thread-incomplete-compaction-background"
	router, store := compactionAccountingThread(t, threadID)
	budget := runtimeutil.NewBudget()
	budget.Configure(runtimeutil.BudgetConfig{LimitTokens: 1})
	router.rolloutBudgetOnce.Do(func() {})
	router.rolloutBudget = budget

	usage := model.AgentUsage{InputTokens: 100, OutputTokens: 20}
	runIncompleteCompaction(t, router, &incompleteCompactionRunner{usage: &usage}, &runtimeCompactRequest{
		ThreadID:   threadID,
		TurnID:     "turn-compact-background",
		Trigger:    compact.TriggerAuto,
		Reason:     compact.ReasonTokenLimit,
		Phase:      compact.PhaseMidTurn,
		RemoteOnly: true,
	})

	record, err := store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		t.Fatalf("Read record error = %v", err)
	}
	extra := record.Metadata.Extra
	// Background requests must not publish foreground token usage: the
	// baseline and cumulative totals stay exactly as they were.
	if total := tokenUsageBreakdownFromMetadata(extra["total_token_usage"]); total == nil || total.TotalTokens != 1100 {
		t.Fatalf("total_token_usage = %#v, want the untouched cumulative total (1100)", extra["total_token_usage"])
	}
	if last := tokenUsageBreakdownFromMetadata(extra["last_token_usage"]); last == nil || last.TotalTokens != 1100 {
		t.Fatalf("last_token_usage = %#v, want the untouched baseline (1100)", extra["last_token_usage"])
	}
	info, ok := extra["token_usage_info"].(map[string]any)
	if !ok {
		t.Fatalf("token_usage_info = %#v", extra["token_usage_info"])
	}
	infoTotal := tokenUsageBreakdownFromMetadata(firstMapValue(info, "total_token_usage", "totalTokenUsage", "total"))
	if infoTotal == nil || infoTotal.TotalTokens != 1100 {
		t.Fatalf("token_usage_info.total_token_usage = %#v, want the untouched baseline", info["total_token_usage"])
	}
	// The reported usage is still charged to the shared rollout budget.
	if !router.rolloutBudgetCharged.Load() || !router.rolloutBudgetExhausted.Load() {
		t.Fatalf("budget charged=%v exhausted=%v, want a budget-only charge for background compaction",
			router.rolloutBudgetCharged.Load(), router.rolloutBudgetExhausted.Load())
	}
}

func TestIncompleteCompactionWithoutUsageIsNotEstimatedLikeRust(t *testing.T) {
	const threadID = "thread-incomplete-compaction-no-usage"
	router, store := compactionAccountingThread(t, threadID)
	budget := runtimeutil.NewBudget()
	budget.Configure(runtimeutil.BudgetConfig{LimitTokens: 1})
	router.rolloutBudgetOnce.Do(func() {})
	router.rolloutBudget = budget

	runIncompleteCompaction(t, router, &incompleteCompactionRunner{}, &runtimeCompactRequest{
		ThreadID:   threadID,
		TurnID:     "turn-compact-no-usage",
		Trigger:    compact.TriggerManual,
		Reason:     compact.ReasonUserRequested,
		Phase:      compact.PhaseStandaloneTurn,
		RemoteOnly: true,
	})

	record, err := store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		t.Fatalf("Read record error = %v", err)
	}
	if total := tokenUsageBreakdownFromMetadata(record.Metadata.Extra["total_token_usage"]); total == nil || total.TotalTokens != 1100 {
		t.Fatalf("total_token_usage = %#v, want the untouched cumulative total (1100)", record.Metadata.Extra["total_token_usage"])
	}
	if router.rolloutBudgetCharged.Load() || router.rolloutBudgetExhausted.Load() {
		t.Fatalf("budget charged=%v exhausted=%v, want no charge for a response that reported no usage",
			router.rolloutBudgetCharged.Load(), router.rolloutBudgetExhausted.Load())
	}
}
