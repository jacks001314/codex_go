package streaming

import (
	"time"

	historycell "codex_go/internal/tui/history_cell"
)

// Rust parity: codex-rs/tui/src/streaming/commit_tick.rs.

type CommitTickScope int

const (
	CommitTickAnyMode CommitTickScope = iota
	CommitTickCatchUpOnly
)

type CommitTickOutput struct {
	Cells         []historycell.HistoryCell
	HasController bool
	AllIdle       bool
}

func RunCommitTick(policy *AdaptiveChunkingPolicy, streamController *StreamController, planStreamController *PlanStreamController, scope CommitTickScope, now time.Time) CommitTickOutput {
	if policy == nil {
		policy = NewAdaptiveChunkingPolicy()
	}
	snapshot := StreamQueueSnapshot(streamController, planStreamController, now)
	decision := policy.Decide(snapshot, now)
	if scope == CommitTickCatchUpOnly && decision.Mode != ChunkingCatchUp {
		return CommitTickOutput{AllIdle: true}
	}
	return ApplyCommitTickPlan(decision.DrainPlan, streamController, planStreamController)
}

func StreamQueueSnapshot(streamController *StreamController, planStreamController *PlanStreamController, now time.Time) QueueSnapshot {
	queuedLines := 0
	var oldestAge *time.Duration
	if streamController != nil {
		queuedLines += streamController.QueuedLines()
		oldestAge = maxDurationPtr(oldestAge, streamController.OldestQueuedAge(now))
	}
	if planStreamController != nil {
		queuedLines += planStreamController.QueuedLines()
		oldestAge = maxDurationPtr(oldestAge, planStreamController.OldestQueuedAge(now))
	}
	return QueueSnapshot{QueuedLines: queuedLines, OldestAge: oldestAge}
}

func ApplyCommitTickPlan(plan DrainPlan, streamController *StreamController, planStreamController *PlanStreamController) CommitTickOutput {
	output := CommitTickOutput{AllIdle: true}
	if streamController != nil {
		output.HasController = true
		cell, idle := drainStreamController(streamController, plan)
		if cell != nil {
			output.Cells = append(output.Cells, cell)
		}
		output.AllIdle = output.AllIdle && idle
	}
	if planStreamController != nil {
		output.HasController = true
		cell, idle := drainPlanStreamController(planStreamController, plan)
		if cell != nil {
			output.Cells = append(output.Cells, cell)
		}
		output.AllIdle = output.AllIdle && idle
	}
	return output
}

func drainStreamController(controller *StreamController, plan DrainPlan) (historycell.HistoryCell, bool) {
	if plan.Kind == DrainBatch {
		return controller.OnCommitTickBatch(plan.Limit)
	}
	return controller.OnCommitTick()
}

func drainPlanStreamController(controller *PlanStreamController, plan DrainPlan) (historycell.HistoryCell, bool) {
	if plan.Kind == DrainBatch {
		return controller.OnCommitTickBatch(plan.Limit)
	}
	return controller.OnCommitTick()
}

func maxDurationPtr(lhs *time.Duration, rhs *time.Duration) *time.Duration {
	if lhs == nil {
		return rhs
	}
	if rhs == nil {
		return lhs
	}
	if *rhs > *lhs {
		return rhs
	}
	return lhs
}
