package tooltiming

import (
	"sync"
	"testing"
	"time"

	"codex_go/criticalpath"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Set(now time.Time) {
	c.mu.Lock()
	c.now = now
	c.mu.Unlock()
}

func newClockedTimings(start time.Time) (*Timings, *fakeClock) {
	clock := &fakeClock{now: start}
	timings := NewTimings()
	timings.clock = clock.Now
	return timings, clock
}

func TestParallelWinnerRetainsItsReviewAndSharedInitialPath(t *testing.T) {
	start := time.Unix(0, 0)
	shared := criticalpath.Path{}.Work(start, start.Add(time.Second), criticalpath.WorkInference)
	timings, clock := newClockedTimings(start)
	batch := NewBatch()
	loser := timings.StartDirectCall("loser", batch, start.Add(time.Second), &shared)
	winner := timings.StartDirectCall("winner", batch, start.Add(time.Second), &shared)
	clock.Set(start.Add(2 * time.Second))
	review := winner.StartReview()
	if review == nil {
		t.Fatal("StartReview() = nil")
	}
	clock.Set(start.Add(5 * time.Second))
	review.End()
	loser.Finish()
	if completed := batch.Completed(); completed != nil {
		t.Fatalf("batch completed before every invocation finished: %+v", completed)
	}
	clock.Set(start.Add(5*time.Second + time.Millisecond))
	completed := winner.Finish()
	if completed == nil {
		t.Fatal("winner.Finish() = nil")
	}
	batchCompleted := batch.Completed()
	if batchCompleted == nil || batchCompleted.Path == nil {
		t.Fatalf("batch.Completed() = %+v", batchCompleted)
	}
	got := batchCompleted.Path.Durations(start, batchCompleted.EndedAt)
	want := criticalpath.Durations{
		Inference:  time.Second,
		AutoReview: 3 * time.Second,
		Tools:      batchCompleted.EndedAt.Sub(start) - 4*time.Second,
	}
	if got != want {
		t.Fatalf("Durations() = %+v, want %+v", got, want)
	}
}

func TestDuplicateIDsAndLateReviewDropDoNotCompleteAnotherInvocation(t *testing.T) {
	start := time.Unix(0, 0)
	timings, clock := newClockedTimings(start)
	batch := NewBatch()
	empty := criticalpath.Path{}
	first := timings.StartDirectCall("same", batch, start, &empty)
	review := first.StartReview()
	second := timings.StartDirectCall("same", batch, start, &empty)
	clock.Set(start.Add(time.Second))
	first.Finish()
	review.End()
	timings.mu.Lock()
	if len(timings.calls["same"]) != 1 {
		timings.mu.Unlock()
		t.Fatalf("calls = %d, want 1", len(timings.calls["same"]))
	}
	activeReviews := timings.calls["same"][0].activeAutoReviews
	timings.mu.Unlock()
	if activeReviews != 0 {
		t.Fatalf("active_auto_reviews = %d, want 0", activeReviews)
	}
	if completed := batch.Completed(); completed != nil {
		t.Fatalf("batch completed before the remaining invocation finished: %+v", completed)
	}
	second.Finish()
	completed := batch.Completed()
	if completed == nil || completed.Path != nil {
		t.Fatalf("batch.Completed() = %+v, want unavailable attribution", completed)
	}
	timings.mu.Lock()
	defer timings.mu.Unlock()
	if len(timings.calls) != 0 {
		t.Fatalf("calls = %d, want 0", len(timings.calls))
	}
}

func TestLateReviewStartDoesNotAttachToReusedCallID(t *testing.T) {
	start := time.Unix(0, 0)
	timings, clock := newClockedTimings(start)
	batch := NewBatch()
	empty := criticalpath.Path{}
	first := timings.StartDirectCall("same", batch, start, &empty)
	review := first.StartReview()
	first.Finish()
	startedAt := start.Add(time.Second)
	clock.Set(startedAt)
	second := timings.StartDirectCall("same", batch, startedAt, &empty)
	if late := first.StartReview(); late != nil {
		t.Fatal("late StartReview() attached to a reused call ID")
	}
	review.End()
	if first.Finish() != nil {
		t.Fatal("first.Finish() completed twice")
	}
	if completed := batch.Completed(); completed != nil {
		t.Fatalf("batch completed before the second invocation finished: %+v", completed)
	}
	clock.Set(startedAt.Add(2 * time.Second))
	second.Finish()
	completed := batch.Completed()
	if completed == nil || completed.Path == nil {
		t.Fatalf("batch.Completed() = %+v", completed)
	}
	got := completed.Path.Durations(startedAt, completed.EndedAt)
	want := criticalpath.Durations{Tools: 2 * time.Second}
	if got != want {
		t.Fatalf("Durations() = %+v, want %+v", got, want)
	}
}

func TestCancellationCompletesOnceAndReviewCanOutliveCall(t *testing.T) {
	start := time.Unix(0, 0)
	timings, clock := newClockedTimings(start)
	batch := NewBatch()
	empty := criticalpath.Path{}
	work := timings.StartDirectCall("cancelled", batch, start, &empty)
	review := work.StartReview()
	clock.Set(start.Add(time.Second))
	first := work.Finish()
	if first == nil {
		t.Fatal("Finish() = nil")
	}
	if work.Finish() != nil {
		t.Fatal("Finish() completed twice")
	}
	review.End()
	completed := batch.Completed()
	if completed == nil || !completed.EndedAt.Equal(first.EndedAt) {
		t.Fatalf("batch.Completed() = %+v, want ended_at %v", completed, first.EndedAt)
	}
	timings.mu.Lock()
	defer timings.mu.Unlock()
	if len(timings.calls) != 0 {
		t.Fatalf("calls = %d, want 0", len(timings.calls))
	}
}

func TestMissingDependencyRemainsUnknown(t *testing.T) {
	start := time.Unix(0, 0).Add(-time.Second)
	timings, clock := newClockedTimings(start)
	batch := NewBatch()
	empty := criticalpath.Path{}
	work := timings.StartDirectCall("call", batch, start, &empty)
	timings.Join("call", start, start.Add(500*time.Millisecond), nil)
	clock.Set(start.Add(time.Second))
	work.Finish()
	completed := batch.Completed()
	if completed == nil || completed.Path == nil {
		t.Fatalf("batch.Completed() = %+v", completed)
	}
	if got := completed.Path.Durations(start, completed.EndedAt).Unknown; got != 500*time.Millisecond {
		t.Fatalf("unknown = %v, want 500ms", got)
	}
}

func TestHumanWaitTakesPrecedenceOverReviewsAndOverlappingWaits(t *testing.T) {
	start := time.Unix(0, 0)
	at := func(seconds int) time.Time { return start.Add(time.Duration(seconds) * time.Second) }
	timings, _ := newClockedTimings(start)
	batch := NewBatch()
	empty := criticalpath.Path{}
	_ = timings.StartDirectCall("call", batch, start, &empty)
	timings.mu.Lock()
	work := timings.calls["call"][0]
	work.recordUntil(at(1))
	work.activeAutoReviews = 1
	work.recordUntil(at(2))
	work.activeUserWaits = 1
	work.recordUntil(at(3))
	work.activeUserWaits = 2
	work.recordUntil(at(4))
	work.activeUserWaits = 1
	work.recordUntil(at(5))
	work.activeUserWaits = 0
	work.recordUntil(at(6))
	work.activeAutoReviews = 0
	work.recordUntil(at(7))
	got := work.path.Durations(start, at(7))
	timings.mu.Unlock()
	want := criticalpath.Durations{
		Tools:      2 * time.Second,
		AutoReview: 2 * time.Second,
		UserWait:   3 * time.Second,
	}
	if got != want {
		t.Fatalf("Durations() = %+v, want %+v", got, want)
	}
}

func TestUserWaitGuardsBalanceAndDoNotFollowReusedCallIDs(t *testing.T) {
	start := time.Unix(0, 0)
	timings, _ := newClockedTimings(start)
	batch := NewBatch()
	empty := criticalpath.Path{}
	first := timings.StartDirectCall("call", batch, start, &empty)
	firstWait := first.StartUserWait()
	nestedWait := first.StartUserWait()
	if firstWait == nil || nestedWait == nil {
		t.Fatal("StartUserWait() = nil")
	}
	timings.mu.Lock()
	if got := timings.calls["call"][0].activeUserWaits; got != 2 {
		timings.mu.Unlock()
		t.Fatalf("active_user_waits = %d, want 2", got)
	}
	timings.mu.Unlock()
	nestedWait.End()
	timings.mu.Lock()
	if got := timings.calls["call"][0].activeUserWaits; got != 1 {
		timings.mu.Unlock()
		t.Fatalf("active_user_waits = %d, want 1", got)
	}
	timings.mu.Unlock()
	first.Finish()
	next := timings.StartDirectCall("call", batch, start, &empty)
	if late := first.StartUserWait(); late != nil {
		t.Fatal("late StartUserWait() attached to a reused call ID")
	}
	firstWait.End()
	timings.mu.Lock()
	if got := timings.calls["call"][0].activeUserWaits; got != 0 {
		timings.mu.Unlock()
		t.Fatalf("active_user_waits = %d, want 0", got)
	}
	timings.mu.Unlock()
	next.Finish()
	if batch.Completed() == nil {
		t.Fatal("batch.Completed() = nil")
	}
}

func TestGateHandoffPublishesReviewHistoryBeforeAdmittingWaiter(t *testing.T) {
	start := time.Unix(0, 0)
	toolStart := start.Add(time.Second)
	shared := criticalpath.Path{}.Work(start, toolStart, criticalpath.WorkInference)
	timings, clock := newClockedTimings(toolStart)
	batch := NewBatch()
	gate := NewGate()
	gate.clock = clock.Now
	releasing := timings.StartTurnDirectCall("releasing", "turn", batch, toolStart, &shared)
	releasingTiming := gate.Admit(releasing, AdmissionExclusive, toolStart)
	clock.Set(start.Add(2 * time.Second))
	timings.mu.Lock()
	releasingInvocation := timings.calls["releasing"][0]
	releasingInvocation.recordUntil(start.Add(2 * time.Second))
	releasingInvocation.activeAutoReviews = 1
	releasingInvocation.recordUntil(start.Add(5 * time.Second))
	releasingInvocation.activeAutoReviews = 0
	timings.mu.Unlock()
	releasingTiming.Finish()
	waiting := timings.StartTurnDirectCall("waiting", "turn", batch, toolStart, &shared)
	clock.Set(start.Add(6 * time.Second))
	waitingTiming := gate.Admit(waiting, AdmissionParallel, toolStart)
	waitingTiming.Finish()
	completed := batch.Completed()
	if completed == nil || completed.Path == nil {
		t.Fatalf("batch.Completed() = %+v", completed)
	}
	got := completed.Path.Durations(start, completed.EndedAt)
	want := criticalpath.Durations{
		Inference:  time.Second,
		AutoReview: 3 * time.Second,
		Tools:      completed.EndedAt.Sub(start) - 4*time.Second,
	}
	if got != want {
		t.Fatalf("Durations() = %+v, want %+v", got, want)
	}
	timings.mu.Lock()
	defer timings.mu.Unlock()
	if len(timings.calls) != 0 {
		t.Fatalf("calls = %d, want 0", len(timings.calls))
	}
}
