package turn

import (
	"testing"
	"time"

	"codex_go/criticalpath"
	"codex_go/tooltiming"
)

func TestCriticalPathCapturePreservesPhasesAndFreezesMissingDependencies(t *testing.T) {
	startedAt := time.Unix(0, 0)
	at := func(ms int) time.Time { return startedAt.Add(time.Duration(ms) * time.Millisecond) }
	timing := NewTimingState()
	timing.MarkTurnStarted(startedAt)
	if got := timing.SnapshotPath(at(10)); got != nil {
		t.Fatalf("SnapshotPath() before enable = %v, want nil", got)
	}
	timing.EnableCriticalPath()
	sampling := timing.BeginSampling(at(10))
	sampling.CloseAt(at(20))
	toolBlocking := timing.BeginToolBlocking(at(20), nil)
	toolBlocking.CloseAt(at(50))
	profile := timing.CompleteProfile(at(60))
	if profile.CriticalPath.Status != CriticalPathStatusComplete || profile.CriticalPath.Durations == nil {
		t.Fatalf("CriticalPath = %+v", profile.CriticalPath)
	}
	got := *profile.CriticalPath.Durations
	want := CriticalPathDurations{
		DurationMS:  60,
		InferenceMS: 10,
		UnknownMS:   30,
		OverheadMS:  20,
	}
	if got != want {
		t.Fatalf("CriticalPathDurations = %+v, want %+v", got, want)
	}
	stale := timing.BeginToolBlocking(at(70), nil)
	stale.CloseAt(at(70))
	if second := timing.CompleteProfile(at(80)); second != profile {
		t.Fatalf("frozen profile changed: %+v != %+v", second, profile)
	}
}

func TestCriticalPathInvalidationAndLateGuardDropCannotRestoreFrozenPath(t *testing.T) {
	startedAt := time.Unix(0, 0)
	timing := NewTimingState()
	timing.EnableCriticalPath()
	timing.MarkTurnStarted(startedAt)
	guard := timing.BeginToolBlocking(startedAt.Add(time.Second), tooltiming.NewBatch())
	timing.InvalidateCriticalPath()
	profile := timing.CompleteProfile(startedAt.Add(2 * time.Second))
	if profile.CriticalPath.Status != CriticalPathStatusIncomplete {
		t.Fatalf("CriticalPath.Status = %q, want incomplete", profile.CriticalPath.Status)
	}
	timing.EnableCriticalPath()
	guard.CloseAt(startedAt.Add(3 * time.Second))
	if got := timing.SnapshotPath(startedAt.Add(4 * time.Second)); got != nil {
		t.Fatalf("SnapshotPath() after invalidation = %v, want nil", got)
	}
}

func TestCriticalPathCompletedBatchGuardRetainsItsPathAcrossTurns(t *testing.T) {
	startedAt := time.Now().Add(-2 * time.Second)
	previousTiming := NewTimingState()
	previousBatch := tooltiming.NewBatch()
	emptyPath := criticalpath.Path{}
	previousCall := previousTiming.DirectToolTimings().StartTurnDirectCall(
		"reviewed", "turn", previousBatch, startedAt, &emptyPath,
	)
	timing := NewTimingState()
	timing.EnableCriticalPath()
	timing.MarkTurnStarted(startedAt)
	batch := tooltiming.NewBatch()
	reviewPath := criticalpath.Path{}.Work(startedAt, startedAt.Add(time.Second), criticalpath.WorkAutoReview)
	call := timing.DirectToolTimings().StartTurnDirectCall(
		"reviewed", "turn", batch, startedAt.Add(time.Second), &reviewPath,
	)
	// A late guard from an earlier turn cannot finish or invalidate the reused ID.
	previousCall.Finish()
	if completed := previousBatch.Completed(); completed == nil || completed.Path == nil {
		t.Fatalf("previous batch.Completed() = %+v", completed)
	}
	if completed := batch.Completed(); completed != nil {
		t.Fatalf("current batch completed before its call finished: %+v", completed)
	}
	guard := timing.BeginToolBlocking(time.Now(), batch)
	// Distinct clock readings keep a batch dependency from tie-breaking
	// against the phase boundary that admitted it.
	time.Sleep(2 * time.Millisecond)
	call.Finish()
	time.Sleep(2 * time.Millisecond)
	guard.CloseAt(time.Now())
	timing.CompleteProfile(time.Now())
	path := timing.SnapshotPath(time.Now())
	if path == nil {
		t.Fatal("SnapshotPath() = nil")
	}
	pathEnd, ok := path.End()
	if !ok {
		t.Fatal("path.End() = false")
	}
	totals := path.Durations(startedAt, pathEnd)
	if totals.AutoReview != time.Second {
		t.Fatalf("auto_review = %v, want 1s", totals.AutoReview)
	}
	if totals.Unknown != 0 {
		t.Fatalf("unknown = %v, want 0", totals.Unknown)
	}
	if totals.Tools < time.Second {
		t.Fatalf("tools = %v, want >= 1s", totals.Tools)
	}
}
