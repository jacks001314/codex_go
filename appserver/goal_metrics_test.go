package appserver

import (
	"context"
	"testing"
	"time"

	"codex_go/model"
	"codex_go/state"
	"codex_go/telemetry"
)

// Mirrors codex-rs/ext/goal/src/metrics.rs GoalMetrics: the created and resumed
// counters are untagged, the terminal counter plus the token-count and
// duration-seconds histograms only fire on a terminal status change, and the
// histograms carry the status tag.
func TestRecordGoalStatusMetricsLikeRust(t *testing.T) {
	metrics := state.NewTaskMetrics()
	router := NewRuntimeRouter(RuntimeServices{TurnMetrics: metrics})

	router.recordGoalCreatedMetric()
	router.recordGoalStatusTransitionMetrics(nil, state.ThreadGoalActive, 0, 0)
	router.recordGoalStatusTransitionMetrics(goalStatusPointer(state.ThreadGoalPaused), state.ThreadGoalActive, 0, 0)
	router.recordGoalStatusTransitionMetrics(goalStatusPointer(state.ThreadGoalActive), state.ThreadGoalActive, 0, 0)
	router.recordGoalStatusTransitionMetrics(goalStatusPointer(state.ThreadGoalActive), state.ThreadGoalComplete, 120, 30)
	router.recordGoalStatusTransitionMetrics(nil, state.ThreadGoalPaused, 0, 0)

	records := metrics.Records()
	if len(records) != 5 {
		t.Fatalf("records = %#v", records)
	}
	if records[0].Name != telemetry.GoalCreatedMetric || records[0].Inc != 1 || len(records[0].Tags) != 0 {
		t.Fatalf("created record = %#v", records[0])
	}
	if records[1].Name != telemetry.GoalResumedMetric || len(records[1].Tags) != 0 {
		t.Fatalf("resumed record = %#v", records[1])
	}
	if records[2].Name != telemetry.GoalCompletedMetric || records[2].Inc != 1 || len(records[2].Tags) != 0 {
		t.Fatalf("completed counter = %#v", records[2])
	}
	if records[3].Name != telemetry.GoalTokenCountMetric || records[3].Value != 120 ||
		records[3].Tags["status"] != string(state.ThreadGoalComplete) {
		t.Fatalf("token histogram = %#v", records[3])
	}
	if records[4].Name != telemetry.GoalDurationSecondsMetric || records[4].Value != 30 ||
		records[4].Tags["status"] != string(state.ThreadGoalComplete) {
		t.Fatalf("duration histogram = %#v", records[4])
	}

	// A nil sink records nothing.
	NewRuntimeRouter(RuntimeServices{}).recordGoalCreatedMetric()
	NewRuntimeRouter(RuntimeServices{}).recordGoalStatusTransitionMetrics(nil, state.ThreadGoalComplete, 1, 1)
}

// An accounting pass that exhausts the goal's token budget reports the
// budget-limited counter and the histograms for the persisted goal.
func TestGoalBudgetLimitEmitsGoalMetricsLikeRust(t *testing.T) {
	router, stateRuntime, threadID := newGoalToolTestRouter(t)
	metrics := state.NewTaskMetrics()
	router.services.TurnMetrics = metrics

	budget := int64(100)
	goal, err := stateRuntime.ReplaceThreadGoal(context.Background(), threadID, "objective", state.ThreadGoalActive, &budget)
	if err != nil || goal == nil {
		t.Fatalf("ReplaceThreadGoal() = %#v, %v", goal, err)
	}
	router.markStateThreadGoalTurnActiveNow(threadID, "turn-1", goal.GoalID, goal.Status)
	router.recordGoalTokenUsage(threadID, "turn-1", model.AgentUsage{InputTokens: 400, OutputTokens: 400})
	outcome := router.accountStateThreadGoalProgress(threadID, "turn-1", time.Now().Add(2*time.Second), state.GoalAccountingActiveOnly)
	if outcome == nil || outcome.Goal == nil || outcome.Goal.Status != state.ThreadGoalBudgetLimited {
		t.Fatalf("accounted goal = %#v", outcome)
	}

	counts := map[string]int{}
	for _, record := range metrics.Records() {
		counts[record.Name]++
	}
	if counts[telemetry.GoalCreatedMetric] != 0 {
		t.Fatalf("an accounting pass recorded the created counter: %#v", counts)
	}
	if counts[telemetry.GoalBudgetLimitedMetric] != 1 {
		t.Fatalf("budget-limited counters = %d (all %#v)", counts[telemetry.GoalBudgetLimitedMetric], counts)
	}
	if counts[telemetry.GoalTokenCountMetric] != 1 || counts[telemetry.GoalDurationSecondsMetric] != 1 {
		t.Fatalf("histograms = %#v", counts)
	}
	for _, record := range metrics.Records() {
		if record.Name != telemetry.GoalTokenCountMetric && record.Name != telemetry.GoalDurationSecondsMetric {
			continue
		}
		if record.Tags["status"] != string(state.ThreadGoalBudgetLimited) {
			t.Fatalf("histogram tags = %#v", record.Tags)
		}
	}
}
