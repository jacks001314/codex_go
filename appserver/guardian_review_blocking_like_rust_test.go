package appserver

import (
	"context"
	"testing"
	"time"

	"codex_go/model"
	"codex_go/state"
	"codex_go/telemetry"
)

// Rust parity: codex-rs/ext/guardian-reviewer/src/blocking_tests.rs. The
// tracker splits the union of a thread's pending approval waits across turns so
// a turn exports one `codex.guardian.turn.blocking_ms` sample (#55591).

func TestGuardianReviewBlockingOverlappingReviewsSpanTurnsLikeRust(t *testing.T) {
	start := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	ms := func(n int) time.Time { return start.Add(time.Duration(n) * time.Millisecond) }
	var interval guardianReviewBlockingInterval
	interval.startTurn(ms(0))
	interval.enter(ms(0))
	interval.enter(ms(10))
	interval.leave(ms(30))
	if interval.elapsed != 0 {
		t.Fatalf("open segment elapsed = %v, want 0 while a wait is still active", interval.elapsed)
	}
	if got := interval.finishTurn(ms(40)); got != 40*time.Millisecond {
		t.Fatalf("first turn blocking = %v, want 40ms", got)
	}
	interval.startTurn(ms(60))
	interval.leave(ms(70))
	if got := interval.finishTurn(ms(80)); got != 10*time.Millisecond {
		t.Fatalf("second turn blocking = %v, want only the 10ms tail", got)
	}
	// A review that starts while idle contributes only after the next turn
	// starts: the wait's start is re-anchored at the turn start.
	interval.enter(ms(100))
	interval.startTurn(ms(200))
	interval.leave(ms(210))
	if got := interval.finishTurn(ms(220)); got != 10*time.Millisecond {
		t.Fatalf("idle review blocking = %v, want 10ms from the turn start", got)
	}
	interval.startTurn(ms(230))
	interval.enter(ms(240))
	interval.leave(ms(250))
	interval.enter(ms(270))
	interval.leave(ms(280))
	if got := interval.finishTurn(ms(300)); got != 20*time.Millisecond {
		t.Fatalf("sequential reviews blocking = %v, want 20ms", got)
	}
	interval.startTurn(ms(310))
	if got := interval.finishTurn(ms(320)); got != 0 {
		t.Fatalf("idle turn blocking = %v, want 0", got)
	}
}

func TestGuardianReviewBlockingLateCompletionPreservesSamplesLikeRust(t *testing.T) {
	start := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	ms := func(n int) time.Time { return start.Add(time.Duration(n) * time.Millisecond) }
	current := ms(0)
	blocking := newGuardianReviewBlocking()
	blocking.now = func() time.Time { return current }
	metrics := state.NewTaskMetrics()

	blocking.startTurn("thread-1", "a", metrics)
	current = ms(10)
	release := blocking.enter("thread-1")
	current = ms(30)
	// Starting turn b finishes turn a's interval into the completed set.
	blocking.startTurn("thread-1", "b", metrics)
	current = ms(50)
	blocking.finishTurn("thread-1", "a", "completed")
	records := guardianMetricRecords(metrics, telemetry.GuardianTurnBlockingDurationMetric)
	if len(records) != 1 || records[0].DurationMS != 20 {
		t.Fatalf("turn a records = %#v, want one 20ms sample", records)
	}
	// A duplicate stop must not export the sample twice.
	current = ms(60)
	blocking.finishTurn("thread-1", "a", "completed")
	if got := len(guardianMetricRecords(metrics, telemetry.GuardianTurnBlockingDurationMetric)); got != 1 {
		t.Fatalf("records after duplicate stop = %d, want 1", got)
	}
	current = ms(70)
	blocking.finishTurn("thread-1", "b", "completed")
	release()
	records = guardianMetricRecords(metrics, telemetry.GuardianTurnBlockingDurationMetric)
	if len(records) != 2 || records[1].DurationMS != 40 {
		t.Fatalf("turn b records = %#v, want a 40ms sample", records)
	}
}

func TestGuardianReviewBlockingOutcomeTagsAndThreadDropLikeRust(t *testing.T) {
	start := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	current := start
	blocking := newGuardianReviewBlocking()
	blocking.now = func() time.Time { return current }
	metrics := state.NewTaskMetrics()

	blocking.startTurn("thread-1", "turn-1", metrics)
	blocking.enter("thread-1")
	current = start.Add(7 * time.Millisecond)
	blocking.finishTurn("thread-1", "turn-1", "aborted")
	records := guardianMetricRecords(metrics, telemetry.GuardianTurnBlockingDurationMetric)
	if len(records) != 1 || records[0].DurationMS != 7 {
		t.Fatalf("aborted records = %#v, want one 7ms sample", records)
	}
	if got := records[0].Tags["outcome"]; got != "aborted" {
		t.Fatalf("outcome tag = %q, want aborted", got)
	}
	// Dropping a closing thread discards saved samples, like Rust's
	// ReviewBlockingTurn drop removing a turn that never finished.
	blocking.startTurn("thread-2", "turn-2", metrics)
	blocking.enter("thread-2")
	current = start.Add(10 * time.Millisecond)
	blocking.forgetThread("thread-2")
	blocking.finishTurn("thread-2", "turn-2", "completed")
	if got := len(guardianMetricRecords(metrics, telemetry.GuardianTurnBlockingDurationMetric)); got != 1 {
		t.Fatalf("records after thread drop = %d, want the earlier sample only", got)
	}
}

// The reviewer enters the tracker around its complete decision, so a review
// that ran inside a turn exports the turn's blocking sample, including the
// full-access fast decision Rust's guard also covers.
func TestGuardianReviewBlockingReviewRecordsTurnSampleLikeRust(t *testing.T) {
	start := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	current := start
	blocking := newGuardianReviewBlocking()
	blocking.now = func() time.Time { return current }
	metrics := state.NewTaskMetrics()
	reviewer := &modelGuardianReviewer{
		agent: guardianAgentFunc(func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
			current = start.Add(125 * time.Millisecond)
			return &model.AgentResponse{Message: guardianDecisionDurationAssessment}, nil
		}),
		store:    state.NewReviewStore(),
		blocking: blocking,
	}
	blocking.startTurn("thread-1", "turn-1", metrics)
	current = start.Add(25 * time.Millisecond)
	if _, _, err := reviewer.Review(context.Background(), "thread-1", "turn-1", "call-1", state.Action{Type: "command", Command: "ls", CWD: t.TempDir()}); err != nil {
		t.Fatalf("review error = %v", err)
	}
	blocking.finishTurn("thread-1", "turn-1", "completed")
	records := guardianMetricRecords(metrics, telemetry.GuardianTurnBlockingDurationMetric)
	if len(records) != 1 || records[0].DurationMS != 100 {
		t.Fatalf("turn blocking records = %#v, want one 100ms review sample", records)
	}
	if got := records[0].Tags["outcome"]; got != "completed" {
		t.Fatalf("outcome tag = %q, want completed", got)
	}
}
