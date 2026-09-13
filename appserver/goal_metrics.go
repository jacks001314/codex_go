package appserver

import (
	"codex_go/state"
	"codex_go/telemetry"
)

// Rust parity: codex-rs/ext/goal/src/metrics.rs GoalMetrics. The created and
// resumed counters carry no tags; the terminal counter depends on the new status
// and is untagged, while the token-count and duration-seconds histograms carry
// the status tag. Every transition is driven by the previous status, which the
// caller takes from the goal it read before mutating.

// recordGoalCreatedMetric mirrors GoalMetrics::record_created.
func (r *RuntimeRouter) recordGoalCreatedMetric() {
	if r == nil || r.services.TurnMetrics == nil {
		return
	}
	r.services.TurnMetrics.Counter(telemetry.GoalCreatedMetric, 1, nil)
}

// recordGoalStatusTransitionMetrics mirrors
// GoalMetrics::record_resumed_if_status_changed plus
// record_terminal_if_status_changed: previous is the goal's status before the
// mutation (nil when no goal existed).
func (r *RuntimeRouter) recordGoalStatusTransitionMetrics(previous *state.ThreadGoalStatus, status state.ThreadGoalStatus, tokensUsed, timeUsedSeconds int64) {
	if r == nil || r.services.TurnMetrics == nil {
		return
	}
	sink := r.services.TurnMetrics
	if previous != nil && status == state.ThreadGoalActive {
		switch *previous {
		case state.ThreadGoalPaused, state.ThreadGoalBlocked, state.ThreadGoalUsageLimited:
			sink.Counter(telemetry.GoalResumedMetric, 1, nil)
		}
	}
	if previous != nil && *previous == status {
		return
	}
	var metric string
	switch status {
	case state.ThreadGoalBlocked:
		metric = telemetry.GoalBlockedMetric
	case state.ThreadGoalUsageLimited:
		metric = telemetry.GoalUsageLimitedMetric
	case state.ThreadGoalBudgetLimited:
		metric = telemetry.GoalBudgetLimitedMetric
	case state.ThreadGoalComplete:
		metric = telemetry.GoalCompletedMetric
	default:
		return
	}
	sink.Counter(metric, 1, nil)
	tags := map[string]string{"status": string(status)}
	sink.Histogram(telemetry.GoalTokenCountMetric, int(tokensUsed), tags)
	sink.Histogram(telemetry.GoalDurationSecondsMetric, int(timeUsedSeconds), tags)
}

// recordGoalStatusTransitionForStateGoal emits the transition metrics for a
// persisted goal.
func (r *RuntimeRouter) recordGoalStatusTransitionForStateGoal(previous *state.ThreadGoalStatus, goal *state.ThreadGoal) {
	if goal == nil {
		return
	}
	r.recordGoalStatusTransitionMetrics(previous, goal.Status, goal.TokensUsed, goal.TimeUsedSeconds)
}

// recordGoalSetMetrics emits the goal metrics for an explicit thread/goal/set,
// deriving the previous status from the goal that existed before the set (nil
// when the set created the goal).
func (r *RuntimeRouter) recordGoalSetMetrics(existing *Goal, goal *Goal) {
	if r == nil || goal == nil {
		return
	}
	status := stateGoalStatus(goal.Status)
	if existing == nil {
		r.recordGoalCreatedMetric()
		r.recordGoalStatusTransitionMetrics(nil, status, goal.TokensUsed, goal.TimeUsedSeconds)
		return
	}
	previous := stateGoalStatus(existing.Status)
	r.recordGoalStatusTransitionMetrics(goalStatusPointer(previous), status, goal.TokensUsed, goal.TimeUsedSeconds)
}

// goalStatusPointer returns a pointer to the status for the transition helper.
func goalStatusPointer(status state.ThreadGoalStatus) *state.ThreadGoalStatus {
	return &status
}
