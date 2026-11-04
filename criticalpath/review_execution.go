package criticalpath

import (
	"sync"
	"time"
)

// ReviewExecution collects exact child execution intervals for one automatic
// review (Rust utils/critical-path review_execution.rs, #59251). Observers must
// Seal before publishing a completed path; later callbacks are ignored.
type ReviewExecution struct {
	mu    sync.Mutex
	state reviewState
}

type reviewState struct {
	observed        bool
	sealed          bool
	unknown         bool
	pending         map[time.Time]struct{}
	pendingAttempts int
	intervals       []reviewInterval
}

type reviewInterval struct {
	start time.Time
	end   time.Time
}

// NewReviewExecution creates an unssealed observer. Copies of the pointer share
// one observation set, matching Rust's Arc<Mutex<State>> identity.
func NewReviewExecution() *ReviewExecution {
	return &ReviewExecution{state: reviewState{pending: map[time.Time]struct{}{}}}
}

// ReviewExecutionAttempt resolves one submission exactly once. The caller must
// call Finish before the attempt goes out of scope; Abandon records an unknown
// outcome for a submission that never completes.
type ReviewExecutionAttempt struct {
	execution *ReviewExecution
	finished  bool
}

// Observe declares that the review's child executions are instrumented, even
// when none run.
func (r *ReviewExecution) Observe() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.state.sealed {
		r.state.observed = true
	}
}

// StartAttempt registers an unresolved submission before asynchronous work can
// be detached.
func (r *ReviewExecution) StartAttempt() *ReviewExecutionAttempt {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.state.sealed {
		r.state.pendingAttempts++
	}
	return &ReviewExecutionAttempt{execution: r}
}

// Finish records whether the submitted child's execution boundaries are known.
func (a *ReviewExecutionAttempt) Finish(known bool) {
	if a == nil || a.finished {
		return
	}
	a.finished = true
	a.execution.attemptFinished(known)
}

// Abandon records an attempt whose submission outcome was never observed.
func (a *ReviewExecutionAttempt) Abandon() {
	a.Finish(false)
}

func (r *ReviewExecution) attemptFinished(known bool) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state.sealed {
		return
	}
	if r.state.pendingAttempts > 0 {
		r.state.pendingAttempts--
	} else {
		r.state.unknown = true
	}
	r.state.unknown = r.state.unknown || !known
}

// Started records the actual child turn start before it can be canceled. Equal
// clock readings cannot distinguish two concurrent attempts and mark the
// observation unknown.
func (r *ReviewExecution) Started(startedAt time.Time) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state.sealed {
		return
	}
	if r.state.pending == nil {
		r.state.pending = map[time.Time]struct{}{}
	}
	if _, exists := r.state.pending[startedAt]; exists {
		r.state.unknown = true
		return
	}
	r.state.pending[startedAt] = struct{}{}
}

// Completed records exact turn bounds. Repeated completion notifications are
// harmless.
func (r *ReviewExecution) Completed(startedAt time.Time, completedAt time.Time) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state.sealed {
		return
	}
	delete(r.state.pending, startedAt)
	if completedAt.Before(startedAt) {
		r.state.unknown = true
		return
	}
	r.state.intervals = append(r.state.intervals, reviewInterval{start: startedAt, end: completedAt})
}

// MarkUnknown records unavailable child attribution instead of reporting it as
// review overhead.
func (r *ReviewExecution) MarkUnknown() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.state.sealed {
		r.state.unknown = true
	}
}

// Seal freezes the evidence. Later callbacks cannot change a completed path.
func (r *ReviewExecution) Seal() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state.sealed = true
}

// reviewOverhead returns the part of [start,end) not covered by the union of
// observed reviewer child execution. The bool is false when attribution or
// dependency history is incomplete.
func reviewOverhead(reviews []*ReviewExecution, start time.Time, end time.Time) (time.Duration, bool) {
	if len(reviews) == 0 || !end.After(start) {
		return 0, false
	}
	intervals := []reviewInterval{}
	for _, review := range reviews {
		if review == nil {
			return 0, false
		}
		review.mu.Lock()
		state := review.state
		if !state.observed || state.unknown || len(state.pending) > 0 || state.pendingAttempts > 0 {
			review.mu.Unlock()
			return 0, false
		}
		for _, interval := range state.intervals {
			left := interval.start
			if left.Before(start) {
				left = start
			}
			right := interval.end
			if right.After(end) {
				right = end
			}
			if left.Before(right) {
				intervals = append(intervals, reviewInterval{start: left, end: right})
			}
		}
		review.mu.Unlock()
	}
	sortReviewIntervals(intervals)
	var execution time.Duration
	coveredUntil := start
	for _, interval := range intervals {
		if !interval.end.After(coveredUntil) {
			continue
		}
		left := interval.start
		if left.Before(coveredUntil) {
			left = coveredUntil
		}
		execution += interval.end.Sub(left)
		coveredUntil = interval.end
	}
	overhead := end.Sub(start) - execution
	if overhead < 0 {
		overhead = 0
	}
	return overhead, true
}

func sortReviewIntervals(intervals []reviewInterval) {
	for i := 1; i < len(intervals); i++ {
		current := intervals[i]
		j := i - 1
		for j >= 0 && intervals[j].start.After(current.start) {
			intervals[j+1] = intervals[j]
			j--
		}
		intervals[j+1] = current
	}
}
