package tea

import (
	"testing"
	"time"

	"codex_go/protocol"
	codextui "codex_go/tui"
)

// TestResumedInProgressTurnKeepsElapsedTimeLikeRust covers Rust #54047
// (chatwidget/replay.rs:146-149 -> ChatWidget::on_replayed_task_started):
// replaying an in-progress turn seeds the Working timer from the turn's
// started_at instead of resetting it to zero.
func TestResumedInProgressTurnKeepsElapsedTimeLikeRust(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	startedAt := now.Add(-90 * time.Second)

	model := NewModel(codextui.NewState(nil), Options{Width: 80, Height: 12})
	model.now = func() time.Time { return now }
	resumed := startedAt.UnixMilli()
	model.applyResumeResponse("thread-a", SessionResumeResponse{
		Status:                 "running",
		WorkingTurnStartedAtMS: &resumed,
	})
	if !model.taskStartedAt.Equal(startedAt) {
		t.Fatalf("resumed task start = %v, want %v", model.taskStartedAt, startedAt)
	}

	// Without the timestamp the timer keeps the zero-time fallback.
	fresh := NewModel(codextui.NewState(nil), Options{Width: 80, Height: 12})
	fresh.now = func() time.Time { return now }
	fresh.applyResumeResponse("thread-b", SessionResumeResponse{Status: "running"})
	if !fresh.taskStartedAt.Equal(now) {
		t.Fatalf("zero-time fallback = %v, want %v", fresh.taskStartedAt, now)
	}
}

// TestReplayedTurnStartedKeepsElapsedTimeLikeRust covers the notification half of
// Rust #54047 (chatwidget/protocol.rs:105-109): a replayed turn/started with a
// started_at timestamp restores the elapsed time, a live turn/started keeps the
// zero-time fallback, and a future timestamp clamps to zero elapsed time.
func TestReplayedTurnStartedKeepsElapsedTimeLikeRust(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

	replayed := now.Add(-2 * time.Minute).UnixMilli()
	replayedEvent := protocol.TurnStarted()
	replayedEvent.TurnID = "turn-1"
	replayedEvent.StartedAtMS = &replayed
	replayedModel := NewModel(codextui.NewState(nil), Options{Width: 80, Height: 12})
	replayedModel.now = func() time.Time { return now }
	replayedModel.Update(ThreadEventMsg{Event: replayedEvent})
	if !replayedModel.taskStartedAt.Equal(now.Add(-2 * time.Minute)) {
		t.Fatalf("replayed task start = %v, want %v", replayedModel.taskStartedAt, now.Add(-2*time.Minute))
	}

	// A live turn/started carries no timestamp and keeps the existing behavior.
	live := NewModel(codextui.NewState(nil), Options{Width: 80, Height: 12})
	live.now = func() time.Time { return now }
	live.Update(ThreadEventMsg{Event: protocol.TurnStarted()})
	if !live.taskStartedAt.Equal(now) {
		t.Fatalf("live task start = %v, want %v", live.taskStartedAt, now)
	}

	// Future timestamps clamp to zero elapsed time (Rust's saturating_sub), so a
	// skewed started_at cannot move the timer ahead of the present.
	future := now.Add(time.Hour).UnixMilli()
	futureEvent := protocol.TurnStarted()
	futureEvent.StartedAtMS = &future
	futureModel := NewModel(codextui.NewState(nil), Options{Width: 80, Height: 12})
	futureModel.now = func() time.Time { return now }
	futureModel.Update(ThreadEventMsg{Event: futureEvent})
	if !futureModel.taskStartedAt.Equal(now) {
		t.Fatalf("future task start = %v, want %v", futureModel.taskStartedAt, now)
	}
}
