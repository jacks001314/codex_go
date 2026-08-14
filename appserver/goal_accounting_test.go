package appserver

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/model"
	"codex_go/state"
	"codex_go/turn"
)

func TestGoalIdleAccountingFlushesBeforeExternalMutation(t *testing.T) {
	router, stateRuntime, threadID := newGoalToolTestRouter(t)
	goal, err := stateRuntime.ReplaceThreadGoal(context.Background(), threadID, "idle accounting", state.ThreadGoalActive, nil)
	if err != nil {
		t.Fatal(err)
	}
	router.markGoalIdleActive(goal.GoalID)
	time.Sleep(1100 * time.Millisecond)
	router.prepareExternalGoalMutation(threadID)

	updated, err := stateRuntime.GetThreadGoal(context.Background(), threadID)
	if err != nil || updated == nil || updated.TimeUsedSeconds < 1 {
		t.Fatalf("goal after idle flush = %#v, %v", updated, err)
	}
}

func TestGoalTokenUsageAccountingAtToolFinish(t *testing.T) {
	router, stateRuntime, threadID := newGoalToolTestRouter(t)
	goal, err := stateRuntime.ReplaceThreadGoal(context.Background(), threadID, "token accounting", state.ThreadGoalActive, nil)
	if err != nil {
		t.Fatal(err)
	}
	router.markStateThreadGoalTurnActiveNow(threadID, "turn-1", goal.GoalID)
	router.recordGoalTokenUsage(threadID, "turn-1", model.AgentUsage{
		InputTokens:       100,
		CachedInputTokens: 20,
		OutputTokens:      30,
	})
	outcome := router.accountStateThreadGoalProgress(threadID, "turn-1", time.Now().Add(2*time.Second), state.GoalAccountingActiveOnly)
	if outcome == nil || outcome.Goal == nil || outcome.Goal.TokensUsed != 110 || outcome.Goal.TimeUsedSeconds < 1 {
		t.Fatalf("accounted goal = %#v", outcome)
	}
}

func TestGoalBudgetLimitSteeringEnqueued(t *testing.T) {
	router, stateRuntime, threadID := newGoalToolTestRouter(t)
	budget := int64(10)
	goal, err := stateRuntime.ReplaceThreadGoal(context.Background(), threadID, "budget steering", state.ThreadGoalActive, &budget)
	if err != nil {
		t.Fatal(err)
	}
	router.markStateThreadGoalTurnActiveNow(threadID, "turn-1", goal.GoalID)
	router.recordGoalTokenUsage(threadID, "turn-1", model.AgentUsage{InputTokens: 20})
	router.accountStateThreadGoalProgress(threadID, "turn-1", time.Now().Add(time.Second), state.GoalAccountingActiveOnly)

	items := router.requireSteerMailbox().Drain(&turn.SteerDrainParams{ThreadID: threadID, TurnID: "turn-1"})
	if len(items) != 1 {
		t.Fatalf("steered items = %#v", items)
	}
	text := ""
	if item, ok := items[0].(map[string]any); ok {
		if content, ok := item["content"].([]map[string]any); ok && len(content) > 0 {
			text, _ = content[0]["text"].(string)
		}
	}
	if !strings.Contains(text, "budget_limited") {
		t.Fatalf("budget steering text = %q", text)
	}
}

func TestGoalToolFinishAccountingSerializesConcurrentUpdates(t *testing.T) {
	router, stateRuntime, threadID := newGoalToolTestRouter(t)
	goal, err := stateRuntime.ReplaceThreadGoal(context.Background(), threadID, "parallel accounting", state.ThreadGoalActive, nil)
	if err != nil {
		t.Fatal(err)
	}
	router.markStateThreadGoalTurnActiveNow(threadID, "turn-1", goal.GoalID)
	router.recordGoalTokenUsage(threadID, "turn-1", model.AgentUsage{InputTokens: 10})
	now := time.Now().Add(time.Second)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			router.accountStateThreadGoalProgress(threadID, "turn-1", now, state.GoalAccountingActiveOnly)
		}()
	}
	wg.Wait()
	updated, err := stateRuntime.GetThreadGoal(context.Background(), threadID)
	if err != nil || updated == nil || updated.TokensUsed != 10 {
		t.Fatalf("parallel accounted goal = %#v, %v", updated, err)
	}
}
