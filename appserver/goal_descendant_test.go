package appserver

import (
	"context"
	"testing"
	"time"

	"codex_go/model"
	"codex_go/session"
	"codex_go/state"
)

// TestDescendantTokenUsageRollsIntoRootGoalLikeRust verifies that token usage
// recorded on a spawned descendant thread is rolled into the root thread's goal
// when the root goal's active progress is accounted (Rust #41183).
func TestDescendantTokenUsageRollsIntoRootGoalLikeRust(t *testing.T) {
	router, stateRuntime, rootThreadID := newGoalToolTestRouter(t)
	ctx := context.Background()
	goal, err := stateRuntime.ReplaceThreadGoal(ctx, rootThreadID, "root goal", state.ThreadGoalActive, nil)
	if err != nil {
		t.Fatal(err)
	}

	// A descendant (subagent) records its own usage and rolls it to the root.
	childUsage := model.AgentUsage{InputTokens: 20, CachedInputTokens: 5, OutputTokens: 8, TotalTokens: 28}
	router.markStateThreadGoalTurnActiveNow(rootThreadID, "root-turn", goal.GoalID)
	router.recordDescendantGoalTokenUsage(rootThreadID, childUsage)

	outcome := router.accountStateThreadGoalProgress(rootThreadID, "root-turn", time.Now().UTC(), state.GoalAccountingActiveOnly)
	if outcome == nil || outcome.Goal == nil {
		t.Fatalf("account outcome = %#v", outcome)
	}
	// 20 input - 5 cached + 8 output = 23 token delta from the descendant.
	if outcome.Goal.TokensUsed != 23 {
		t.Fatalf("root goal tokens used = %d, want 23", outcome.Goal.TokensUsed)
	}
}

// TestDescendantTokenUsageBaselineResetsOnGoalChangeLikeRust verifies that
// descendant usage already accounted to a goal does not carry across when the
// thread's active goal is replaced (Rust #41183 baseline reset).
func TestDescendantTokenUsageBaselineResetsOnGoalChangeLikeRust(t *testing.T) {
	router, stateRuntime, threadID := newGoalToolTestRouter(t)
	ctx := context.Background()
	firstGoal, err := stateRuntime.ReplaceThreadGoal(ctx, threadID, "first goal", state.ThreadGoalActive, nil)
	if err != nil {
		t.Fatal(err)
	}

	router.markStateThreadGoalTurnActiveNow(threadID, "turn-1", firstGoal.GoalID)
	router.recordDescendantGoalTokenUsage(threadID, model.AgentUsage{InputTokens: 20, CachedInputTokens: 5, OutputTokens: 8, TotalTokens: 28})
	outcome := router.accountStateThreadGoalProgress(threadID, "turn-1", time.Now().UTC(), state.GoalAccountingActiveOnly)
	if outcome == nil || outcome.Goal == nil || outcome.Goal.TokensUsed != 23 {
		t.Fatalf("first goal accounted = %#v", outcome)
	}

	// Replace the goal; descendant usage recorded after the replacement must not
	// reuse the prior goal's accounted baseline.
	secondGoal, err := stateRuntime.ReplaceThreadGoal(ctx, threadID, "second goal", state.ThreadGoalActive, nil)
	if err != nil {
		t.Fatal(err)
	}
	// re-anchor the second goal's turn (goal change resets the descendant baseline).
	router.markStateThreadGoalTurnActiveNow(threadID, "turn-2", secondGoal.GoalID)
	router.recordDescendantGoalTokenUsage(threadID, model.AgentUsage{InputTokens: 6, CachedInputTokens: 1, OutputTokens: 3, TotalTokens: 9})
	outcome = router.accountStateThreadGoalProgress(threadID, "turn-2", time.Now().UTC(), state.GoalAccountingActiveOnly)
	if outcome == nil || outcome.Goal == nil {
		t.Fatalf("second goal accounted = %#v", outcome)
	}
	// 6 - 1 + 3 = 8 token delta from the descendant after the goal change.
	if outcome.Goal.TokensUsed != 8 {
		t.Fatalf("second goal tokens used = %d, want 8", outcome.Goal.TokensUsed)
	}
}

// TestDescendantTokenUsageRoutedToRootThreadGoalLikeRust verifies that a
// subagent thread's token usage (recorded through
// recordGoalTokenUsageWithDescendants) is attributed to its root ancestor
// thread's goal (Rust #41183 root_accounting_state routing).
func TestDescendantTokenUsageRoutedToRootThreadGoalLikeRust(t *testing.T) {
	router, stateRuntime, rootThreadID := newGoalToolTestRouter(t)
	ctx := context.Background()
	goal, err := stateRuntime.ReplaceThreadGoal(ctx, rootThreadID, "root goal", state.ThreadGoalActive, nil)
	if err != nil {
		t.Fatal(err)
	}
	router.markStateThreadGoalTurnActiveNow(rootThreadID, "root-turn", goal.GoalID)

	// Create a subagent thread whose parent is the root thread, and record its
	// usage through the descendant-aware helper.
	now := time.Now().UTC()
	childID := "child-thread"
	store := router.services.ThreadRouter.store
	if err := store.Save(&session.Record{
		ID:             session.ThreadID(childID),
		SessionID:      childID,
		ParentThreadID: session.ThreadID(rootThreadID),
		CreatedAt:      now,
		UpdatedAt:      now,
		RecencyAt:      now,
		Metadata:       session.Metadata{CWD: t.TempDir(), ModelProvider: "openai", HistoryMode: string(ThreadHistoryLegacy)},
	}); err != nil {
		t.Fatalf("store.Save child = %v", err)
	}

	router.recordGoalTokenUsageWithDescendants(childID, "child-turn", model.AgentUsage{InputTokens: 14, CachedInputTokens: 4, OutputTokens: 6, TotalTokens: 20})
	outcome := router.accountStateThreadGoalProgress(rootThreadID, "root-turn", time.Now().UTC(), state.GoalAccountingActiveOnly)
	if outcome == nil || outcome.Goal == nil {
		t.Fatalf("root account outcome = %#v", outcome)
	}
	// 14 - 4 + 6 = 16 token delta rolled from the child to the root goal.
	if outcome.Goal.TokensUsed != 16 {
		t.Fatalf("root goal tokens used = %d, want 16", outcome.Goal.TokensUsed)
	}
}
