package state

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestThreadGoalStoreMatchesRustUpdateAndAccountingSemantics(t *testing.T) {
	runtime := newGoalTestRuntime(t)
	ctx := context.Background()
	budget := int64(100)
	goal, err := runtime.ReplaceThreadGoal(ctx, "thread-1", "ship parity", ThreadGoalActive, &budget)
	if err != nil {
		t.Fatal(err)
	}
	if goal.Status != ThreadGoalActive || goal.TokensUsed != 0 || goal.GoalID == "" {
		t.Fatalf("initial goal = %#v", goal)
	}
	goalID := goal.GoalID

	outcome, err := runtime.AccountThreadGoalUsage(ctx, "thread-1", 7, 60, GoalAccountingActiveOnly, &goalID)
	if err != nil || !outcome.Updated || outcome.Goal.TokensUsed != 60 || outcome.Goal.TimeUsedSeconds != 7 {
		t.Fatalf("first accounting = %#v, %v", outcome, err)
	}
	lowered := int64(50)
	objective := "ship exact parity"
	goal, err = runtime.UpdateThreadGoal(ctx, "thread-1", GoalUpdate{
		Objective:      &objective,
		TokenBudget:    &lowered,
		TokenBudgetSet: true,
		ExpectedGoalID: &goalID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if goal.GoalID != goalID || goal.Objective != objective || goal.Status != ThreadGoalBudgetLimited || goal.TokensUsed != 60 {
		t.Fatalf("lowered-budget goal = %#v", goal)
	}

	paused := ThreadGoalPaused
	goal, err = runtime.UpdateThreadGoal(ctx, "thread-1", GoalUpdate{Status: &paused, ExpectedGoalID: &goalID})
	if err != nil {
		t.Fatal(err)
	}
	if goal.Status != ThreadGoalBudgetLimited {
		t.Fatalf("pausing budget-limited goal changed status to %q", goal.Status)
	}

	wrongID := "stale-goal-id"
	changed, err := runtime.UpdateThreadGoal(ctx, "thread-1", GoalUpdate{Objective: stringPointer("stale"), ExpectedGoalID: &wrongID})
	if err != nil || changed != nil {
		t.Fatalf("stale update = %#v, %v", changed, err)
	}
	unchanged, err := runtime.GetThreadGoal(ctx, "thread-1")
	if err != nil || unchanged.Objective != objective {
		t.Fatalf("goal after stale update = %#v, %v", unchanged, err)
	}

	outcome, err = runtime.AccountThreadGoalUsage(ctx, "thread-1", 2, 10, GoalAccountingActiveStatusOnly, nil)
	if err != nil || outcome.Updated || outcome.Goal.TokensUsed != 60 {
		t.Fatalf("active-status-only accounting = %#v, %v", outcome, err)
	}
	outcome, err = runtime.AccountThreadGoalUsage(ctx, "thread-1", 2, 10, GoalAccountingActiveOnly, nil)
	if err != nil || !outcome.Updated || outcome.Goal.TokensUsed != 70 || outcome.Goal.TimeUsedSeconds != 9 {
		t.Fatalf("active-only in-flight accounting = %#v, %v", outcome, err)
	}

	active := ThreadGoalActive
	goal, err = runtime.UpdateThreadGoal(ctx, "thread-1", GoalUpdate{
		Status:         &active,
		TokenBudgetSet: true,
		ExpectedGoalID: &goalID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if goal.Status != ThreadGoalActive || goal.TokenBudget != nil {
		t.Fatalf("cleared-budget goal = %#v", goal)
	}
}

func TestThreadGoalStoreInsertPauseUsageLimitAndConcurrentAccounting(t *testing.T) {
	runtime := newGoalTestRuntime(t)
	ctx := context.Background()
	budget := int64(1_000)
	goal, err := runtime.InsertThreadGoal(ctx, "thread-2", "count every token", ThreadGoalActive, &budget)
	if err != nil || goal == nil {
		t.Fatalf("insert = %#v, %v", goal, err)
	}
	if duplicate, err := runtime.InsertThreadGoal(ctx, "thread-2", "do not replace", ThreadGoalActive, nil); err != nil || duplicate != nil {
		t.Fatalf("duplicate active insert = %#v, %v", duplicate, err)
	}

	const workers = 12
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, accountErr := runtime.AccountThreadGoalUsage(ctx, "thread-2", 1, 10, GoalAccountingActiveOnly, nil)
			errs <- accountErr
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	goal, err = runtime.GetThreadGoal(ctx, "thread-2")
	if err != nil || goal.TokensUsed != workers*10 || goal.TimeUsedSeconds != workers {
		t.Fatalf("concurrently accounted goal = %#v, %v", goal, err)
	}

	goal, err = runtime.PauseActiveThreadGoal(ctx, "thread-2")
	if err != nil || goal.Status != ThreadGoalPaused {
		t.Fatalf("paused goal = %#v, %v", goal, err)
	}
	if unchanged, err := runtime.PauseActiveThreadGoal(ctx, "thread-2"); err != nil || unchanged != nil {
		t.Fatalf("second pause = %#v, %v", unchanged, err)
	}
	limited, err := runtime.UsageLimitActiveThreadGoal(ctx, "thread-2")
	if err != nil || limited != nil {
		t.Fatalf("usage-limit paused goal = %#v, %v", limited, err)
	}

	complete := ThreadGoalComplete
	goal, err = runtime.UpdateThreadGoal(ctx, "thread-2", GoalUpdate{Status: &complete})
	if err != nil || goal.Status != ThreadGoalComplete {
		t.Fatalf("completed goal = %#v, %v", goal, err)
	}
	replacement, err := runtime.InsertThreadGoal(ctx, "thread-2", "new objective", ThreadGoalActive, nil)
	if err != nil || replacement == nil || replacement.GoalID == goal.GoalID || replacement.TokensUsed != 0 {
		t.Fatalf("replacement after completion = %#v, %v", replacement, err)
	}
}

func TestThreadGoalSnapshotDefersContinuationAndDeleteCascades(t *testing.T) {
	runtime := newGoalTestRuntime(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	budget := int64(500)
	snapshot := &ThreadGoal{
		ThreadID: "fork-target", GoalID: "inherited-goal", Objective: "finish source objective",
		Status: ThreadGoalBlocked, TokenBudget: &budget, TokensUsed: 123, TimeUsedSeconds: 45,
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now,
	}
	if err := runtime.ReplaceThreadGoalSnapshot(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	got, err := runtime.GetThreadGoal(ctx, "fork-target")
	if err != nil || got.GoalID != snapshot.GoalID || got.Status != snapshot.Status || got.TokensUsed != snapshot.TokensUsed || !got.CreatedAt.Equal(snapshot.CreatedAt) {
		t.Fatalf("snapshot = %#v, %v", got, err)
	}
	deferred, err := runtime.HasThreadGoalContinuationDeferral(ctx, "fork-target")
	if err != nil || !deferred {
		t.Fatalf("deferral = %v, %v", deferred, err)
	}
	if err := runtime.ClearThreadGoalContinuationDeferral(ctx, "fork-target"); err != nil {
		t.Fatal(err)
	}
	deferred, err = runtime.HasThreadGoalContinuationDeferral(ctx, "fork-target")
	if err != nil || deferred {
		t.Fatalf("cleared deferral = %v, %v", deferred, err)
	}
	if err := runtime.ReplaceThreadGoalSnapshot(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	deleted, err := runtime.DeleteThreadGoal(ctx, "fork-target")
	if err != nil || deleted == nil || deleted.GoalID != snapshot.GoalID {
		t.Fatalf("deleted goal = %#v, %v", deleted, err)
	}
	deferred, err = runtime.HasThreadGoalContinuationDeferral(ctx, "fork-target")
	if err != nil || deferred {
		t.Fatalf("cascaded deferral = %v, %v", deferred, err)
	}
	if deleted, err := runtime.DeleteThreadGoal(ctx, "fork-target"); err != nil || deleted != nil {
		t.Fatalf("second delete = %#v, %v", deleted, err)
	}
}

func TestThreadGoalZeroBudgetImmediatelyLimitsActiveState(t *testing.T) {
	runtime := newGoalTestRuntime(t)
	zero := int64(0)
	goal, err := runtime.ReplaceThreadGoal(nil, "thread-zero", "state-level invariant", ThreadGoalActive, &zero)
	if err != nil {
		t.Fatal(err)
	}
	if goal.Status != ThreadGoalBudgetLimited {
		t.Fatalf("status = %q, want %q", goal.Status, ThreadGoalBudgetLimited)
	}
}

func newGoalTestRuntime(t *testing.T) *StateRuntime {
	t.Helper()
	config, err := NewSqliteConfig(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := InitStateRuntime(context.Background(), config, "test-provider")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	return runtime
}

func stringPointer(value string) *string { return &value }
