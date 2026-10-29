package appserver

import (
	"strings"
	"sync"
	"time"

	"codex_go/telemetry"
)

// Rust parity: codex-rs/ext/guardian-reviewer/src/blocking.rs. The tracker
// splits the union of a thread's pending approval waits across the turns that
// service them: turn completion exports one sample, while pending reviews
// survive idle gaps and turn changes. Saved durations are removed when their
// turn finishes or its handle drops (Rust's ReviewBlockingTurn drop).
//
// Rust keeps one ReviewBlocking in each thread's extension data and one
// ReviewBlockingTurn in each turn's extension data, and records the sample
// through the servicing turn's SessionTelemetry. Go's app-server owns the
// threads, so the router keeps the same state keyed by thread and turn and
// records through the router's turn metric sink; the turn-scoped session
// attribution Rust attaches through SessionTelemetry is not part of Go's
// metric sink, so Go exports the same `outcome` tag without it.

// guardianReviewBlocking tracks pending approvals independently of the turn
// currently servicing them.
type guardianReviewBlocking struct {
	mu      sync.Mutex
	now     func() time.Time
	threads map[string]*guardianReviewBlockingThread
}

// guardianReviewBlockingThread mirrors Rust's blocking::State plus the metric
// sink each turn handle carries in Rust.
type guardianReviewBlockingThread struct {
	interval  guardianReviewBlockingInterval
	turn      string
	completed map[string]time.Duration
	sinks     map[string]telemetry.TurnMetricSink
}

// guardianReviewBlockingInterval mirrors Rust's blocking::Interval: the union
// of the thread's active approval waits, with `since` set only while at least
// one wait is active.
type guardianReviewBlockingInterval struct {
	active  int
	since   time.Time
	elapsed time.Duration
}

func newGuardianReviewBlocking() *guardianReviewBlocking {
	return &guardianReviewBlocking{
		now:     time.Now,
		threads: map[string]*guardianReviewBlockingThread{},
	}
}

func (b *guardianReviewBlocking) clock() time.Time {
	if b != nil && b.now != nil {
		return b.now()
	}
	return time.Now()
}

func (b *guardianReviewBlocking) threadLocked(threadID string) *guardianReviewBlockingThread {
	thread := b.threads[threadID]
	if thread == nil {
		thread = &guardianReviewBlockingThread{
			completed: map[string]time.Duration{},
			sinks:     map[string]telemetry.TurnMetricSink{},
		}
		b.threads[threadID] = thread
	}
	return thread
}

// enter marks the start of one pending approval wait for the thread and returns
// the release that marks its end. Rust's BlockingGuard leaves the interval when
// the review decision returns.
func (b *guardianReviewBlocking) enter(threadID string) func() {
	if b == nil {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	now := b.clock()
	b.mu.Lock()
	thread := b.threadLocked(threadID)
	thread.interval.enter(now)
	b.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			leftAt := b.clock()
			b.mu.Lock()
			thread.interval.leave(leftAt)
			b.mu.Unlock()
		})
	}
}

// startTurn starts the thread's next turn sample. A previous current turn's
// interval is finished into the completed set so a late finish_turn still
// exports its own sample.
func (b *guardianReviewBlocking) startTurn(threadID, turnID string, sink telemetry.TurnMetricSink) {
	if b == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	if threadID == "" || turnID == "" {
		return
	}
	now := b.clock()
	b.mu.Lock()
	defer b.mu.Unlock()
	thread := b.threadLocked(threadID)
	if thread.turn != "" {
		thread.completed[thread.turn] = thread.interval.finishTurn(now)
	}
	thread.turn = turnID
	thread.interval.startTurn(now)
	thread.sinks[turnID] = sink
}

// finishTurn exports the turn's blocking sample with its outcome (Rust's
// "completed" or "aborted") and forgets the turn. A turn that already finished
// (or never started) records nothing, matching Rust's duplicate-stop behavior.
func (b *guardianReviewBlocking) finishTurn(threadID, turnID, outcome string) {
	if b == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	if threadID == "" || turnID == "" {
		return
	}
	now := b.clock()
	b.mu.Lock()
	thread := b.threads[threadID]
	if thread == nil {
		b.mu.Unlock()
		return
	}
	var (
		elapsed time.Duration
		found   bool
	)
	if thread.turn == turnID {
		thread.turn = ""
		elapsed = thread.interval.finishTurn(now)
		found = true
	} else {
		elapsed, found = thread.completed[turnID]
		delete(thread.completed, turnID)
	}
	sink := thread.sinks[turnID]
	delete(thread.sinks, turnID)
	b.mu.Unlock()
	if !found || sink == nil {
		return
	}
	sink.RecordDuration(telemetry.GuardianTurnBlockingDurationMetric, elapsed, map[string]string{"outcome": outcome})
}

// forgetThread drops a closing thread's tracker, discarding saved samples the
// way Rust's ReviewBlockingTurn drop removes a turn that never finished.
func (b *guardianReviewBlocking) forgetThread(threadID string) {
	if b == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	b.mu.Lock()
	delete(b.threads, threadID)
	b.mu.Unlock()
}

// startTurn mirrors Interval::start_turn: the new turn owns the interval from
// now on, while a wait already active keeps running and an idle wait charges
// only the time after this turn started.
func (i *guardianReviewBlockingInterval) startTurn(now time.Time) {
	i.elapsed = 0
	if i.active > 0 {
		i.since = now
		return
	}
	i.since = time.Time{}
}

// finishTurn mirrors Interval::finish_turn: it adds the open segment, then
// returns and resets the accumulated duration.
func (i *guardianReviewBlockingInterval) finishTurn(now time.Time) time.Duration {
	if !i.since.IsZero() {
		i.elapsed += guardianReviewBlockingSegment(now, i.since)
		i.since = time.Time{}
	}
	elapsed := i.elapsed
	i.elapsed = 0
	return elapsed
}

func (i *guardianReviewBlockingInterval) enter(now time.Time) {
	if i.active == 0 {
		i.since = now
	}
	i.active++
}

func (i *guardianReviewBlockingInterval) leave(now time.Time) {
	if i.active > 0 {
		i.active--
	}
	if i.active == 0 && !i.since.IsZero() {
		i.elapsed += guardianReviewBlockingSegment(now, i.since)
		i.since = time.Time{}
	}
}

// guardianReviewBlockingSegment mirrors Instant::saturating_duration_since: a
// clock that does not advance contributes nothing instead of a negative span.
func guardianReviewBlockingSegment(now, since time.Time) time.Duration {
	elapsed := now.Sub(since)
	if elapsed <= 0 {
		return 0
	}
	return elapsed
}

// requireGuardianReviewBlocking returns the router's thread-owned tracker,
// which the Guardian reviewer and the turn lifecycle share (Rust's per-thread
// ReviewBlocking lives in the thread's extension data).
func (r *RuntimeRouter) requireGuardianReviewBlocking() *guardianReviewBlocking {
	if r == nil {
		return nil
	}
	r.guardianBlockingOnce.Do(func() {
		r.guardianBlocking = newGuardianReviewBlocking()
	})
	return r.guardianBlocking
}

// beginGuardianBlockingTurn mirrors Rust's turn-start lifecycle hook: the turn
// captures the metric sink its sample is exported through.
func (r *RuntimeRouter) beginGuardianBlockingTurn(threadID, turnID string) {
	if r == nil {
		return
	}
	r.requireGuardianReviewBlocking().startTurn(threadID, turnID, r.services.TurnMetrics)
}

// beginGuardianTurn runs the Guardian turn-start hooks. Rust #45672
// (ext/guardian-v2/src/sync_reviewer/mod.rs) consolidates the reviewer's turn
// and thread lifecycle registration in one install; Go's router already owns
// both, so one entry point starts the blocking sample and drops any denial
// window left on the new turn before reviews can be recorded against it.
func (r *RuntimeRouter) beginGuardianTurn(threadID, turnID string) {
	r.beginGuardianBlockingTurn(threadID, turnID)
	r.clearGuardianDenialsForTurn(turnID)
}

// endGuardianBlockingTurn mirrors Rust's turn stop/abort hook, exporting the
// turn's blocking sample tagged with the outcome.
func (r *RuntimeRouter) endGuardianBlockingTurn(threadID, turnID, outcome string) {
	if r == nil {
		return
	}
	r.requireGuardianReviewBlocking().finishTurn(threadID, turnID, outcome)
}

// endGuardianTurn runs the Guardian turn-stop and turn-abort hooks. Rust #45672
// moved extension-owned denial cleanup out of core task handling and into these
// hooks, so a completed or interrupted turn never leaves accounting behind for
// a later review (or a reused turn id) to inherit.
func (r *RuntimeRouter) endGuardianTurn(threadID, turnID, outcome string) {
	r.endGuardianBlockingTurn(threadID, turnID, outcome)
	r.clearGuardianDenialsForTurn(turnID)
}

// clearGuardianDenialsForTurn drops the reviewer's denial window for one turn.
// The breaker is owned by the model reviewer, so a stub or missing reviewer has
// no accounting to clear.
func (r *RuntimeRouter) clearGuardianDenialsForTurn(turnID string) {
	if r == nil {
		return
	}
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return
	}
	reviewer, ok := r.services.GuardianReviewer.(*modelGuardianReviewer)
	if !ok || reviewer == nil || reviewer.breaker == nil {
		return
	}
	reviewer.breaker.ClearTurn(turnID)
}

// forgetGuardianBlocking drops a closing thread's tracker, discarding saved
// samples like Rust dropping the thread's extension data.
func (r *RuntimeRouter) forgetGuardianBlocking(threadID string) {
	if r == nil {
		return
	}
	r.requireGuardianReviewBlocking().forgetThread(threadID)
}
