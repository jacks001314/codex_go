// Package tooltiming records direct-tool invocations, automated reviews, human
// waits, execution-lock handoffs, and batch completion for critical-path turn
// attribution (Rust codex-direct-tool-timing #57201/#57207).
//
// Prefixes and dependency paths can contain any work kind; this recorder adds
// tool, review, and human-wait intervals, or unknown work for a dependency wait
// whose history is unavailable. Review callbacks retain their invocation guard
// and bracket blocking automated reviews with the blocking guard's lifetime.
// Execution-lock admission timing is tracked separately from invocation timing.
package tooltiming

import (
	"sync"
	"time"

	"codex_go/criticalpath"
)

// CompletedWork is the completion time and dependency history for one direct
// invocation.
type CompletedWork struct {
	EndedAt time.Time
	// Path is nil when attribution was invalidated, for example by a reused
	// external call ID.
	Path *criticalpath.Path
}

// Batch tracks the last-finishing invocation after every invocation in a batch
// completes.
type Batch struct {
	mu      sync.Mutex
	pending int
	latest  *CompletedWork
}

// NewBatch returns an empty batch.
func NewBatch() *Batch {
	return &Batch{}
}

// Completed returns the batch result only when no invocation remains active.
func (b *Batch) Completed() *CompletedWork {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.pending != 0 || b.latest == nil {
		return nil
	}
	completed := *b.latest
	return &completed
}

func (b *Batch) started() {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.pending++
	b.mu.Unlock()
}

func (b *Batch) record(work *CompletedWork) {
	if b == nil || work == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.pending > 0 {
		b.pending--
	}
	if b.latest == nil || b.latest.EndedAt.Before(work.EndedAt) {
		completed := *work
		b.latest = &completed
	}
}

// identity is non-zero-sized so distinct guards never share an address.
type identity struct {
	marker byte
}

type runtimeOwner struct {
	turnID string
	guard  *ToolGuard
}

type invocation struct {
	runtimeOwner      *runtimeOwner
	identity          *identity
	path              *criticalpath.Path
	segmentStartedAt  time.Time
	activeAutoReviews int
	activeUserWaits   int
	batch             *Batch
}

// Timings owns direct invocations; duplicate external call IDs invalidate
// attribution.
type Timings struct {
	mu    sync.Mutex
	calls map[string][]*invocation
	clock func() time.Time
}

// NewTimings returns an empty invocation registry.
func NewTimings() *Timings {
	return &Timings{calls: map[string][]*invocation{}, clock: time.Now}
}

func (t *Timings) now() time.Time {
	if t.clock == nil {
		return time.Now()
	}
	return t.clock()
}

// ToolGuard owns one direct invocation. Finish is idempotent; later blocking
// guard releases cannot affect another call.
type ToolGuard struct {
	identity *identity
	timings  *Timings
	callID   string
}

type blockingKind uint8

const (
	blockingAutoReview blockingKind = iota
	blockingUserWait
)

// BlockingGuard records a blocking interval until End is called. User waits
// take precedence over reviews.
type BlockingGuard struct {
	kind     blockingKind
	identity *identity
	timings  *Timings
	callID   string
	mu       sync.Mutex
	ended    bool
}

// StartDirectCall starts an invocation with its causal prefix, or without
// attribution when the prefix is unavailable.
func (t *Timings) StartDirectCall(callID string, batch *Batch, startedAt time.Time, prefix *criticalpath.Path) *ToolGuard {
	return t.startDirectCall(callID, batch, startedAt, prefix, false, "")
}

// StartTurnDirectCall starts a direct call owned by the given turn so a review
// can find it later without relying on the external call ID alone.
func (t *Timings) StartTurnDirectCall(callID string, turnID string, batch *Batch, startedAt time.Time, prefix *criticalpath.Path) *ToolGuard {
	return t.startDirectCall(callID, batch, startedAt, prefix, true, turnID)
}

func (t *Timings) startDirectCall(callID string, batch *Batch, startedAt time.Time, prefix *criticalpath.Path, owned bool, turnID string) *ToolGuard {
	if batch == nil {
		batch = NewBatch()
	}
	batch.started()
	var path *criticalpath.Path
	if prefix != nil {
		previous := startedAt
		if end, ok := prefix.End(); ok {
			previous = end
		}
		work := prefix.Work(previous, startedAt, criticalpath.WorkTools)
		path = &work
	}
	workIdentity := &identity{marker: 1}
	t.mu.Lock()
	calls := t.calls[callID]
	if len(calls) > 0 {
		// External call IDs are not guaranteed unique. Keep each guard's
		// completion separate, but do not guess which invocation a lookup names.
		for _, invocation := range calls {
			invocation.path = nil
		}
		path = nil
	}
	invocation := &invocation{
		identity:         workIdentity,
		path:             path,
		segmentStartedAt: startedAt,
		batch:            batch,
	}
	if owned {
		invocation.runtimeOwner = &runtimeOwner{turnID: turnID}
	}
	t.calls[callID] = append(calls, invocation)
	guard := &ToolGuard{identity: workIdentity, timings: t, callID: callID}
	if owned {
		invocation.runtimeOwner.guard = guard
	}
	t.mu.Unlock()
	return guard
}

// Join records a dependency wait for a unique call ID; missing history becomes
// unknown time.
func (t *Timings) Join(callID string, start time.Time, end time.Time, dependency *criticalpath.Path) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if invocation := t.uniqueWorkLocked(callID); invocation != nil {
		invocation.join(start, end, dependency)
	}
}

// StartReviewForTurn starts a review only for the invocation owned by the turn.
func (t *Timings) StartReviewForTurn(callID string, turnID string) *BlockingGuard {
	owner := t.OwnerForTurn(callID, turnID)
	if owner == nil {
		return nil
	}
	return owner.StartReview()
}

// StartUserWaitForTurn starts a human wait only for the invocation owned by the
// turn.
func (t *Timings) StartUserWaitForTurn(callID string, turnID string) *BlockingGuard {
	owner := t.OwnerForTurn(callID, turnID)
	if owner == nil {
		return nil
	}
	return owner.StartUserWait()
}

// OwnerForTurn reports the invocation that the turn owns for this call ID.
func (t *Timings) OwnerForTurn(callID string, turnID string) *ToolGuard {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	invocation := t.uniqueWorkLocked(callID)
	if invocation == nil || invocation.runtimeOwner == nil {
		return nil
	}
	if invocation.runtimeOwner.turnID != turnID {
		return nil
	}
	return invocation.runtimeOwner.guard
}

func (t *Timings) uniqueWorkLocked(callID string) *invocation {
	calls := t.calls[callID]
	if len(calls) != 1 {
		return nil
	}
	return calls[0]
}

// Finish completes this invocation once; subsequent calls have no effect.
func (g *ToolGuard) Finish() *CompletedWork {
	if g == nil {
		return nil
	}
	return g.timings.finish(g.callID, g.identity)
}

func (t *Timings) finish(callID string, workIdentity *identity) *CompletedWork {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	calls := t.calls[callID]
	index := -1
	for i, invocation := range calls {
		if invocation.identity == workIdentity {
			index = i
			break
		}
	}
	if index < 0 {
		t.mu.Unlock()
		return nil
	}
	invocation := calls[index]
	calls[index] = calls[len(calls)-1]
	calls = calls[:len(calls)-1]
	if len(calls) == 0 {
		delete(t.calls, callID)
	} else {
		t.calls[callID] = calls
	}
	t.mu.Unlock()
	endedAt := t.now()
	invocation.recordUntil(endedAt)
	completed := &CompletedWork{EndedAt: endedAt, Path: invocation.path}
	invocation.batch.record(completed)
	result := *completed
	return &result
}

// StartReview starts an automatic review only for this invocation, including
// after external call ID reuse.
func (g *ToolGuard) StartReview() *BlockingGuard {
	return g.startBlocking(blockingAutoReview)
}

// StartUserWait starts a human wait for this invocation, taking precedence over
// automated review.
func (g *ToolGuard) StartUserWait() *BlockingGuard {
	return g.startBlocking(blockingUserWait)
}

func (g *ToolGuard) startBlocking(kind blockingKind) *BlockingGuard {
	if g == nil {
		return nil
	}
	t := g.timings
	t.mu.Lock()
	defer t.mu.Unlock()
	invocation := t.uniqueWorkLocked(g.callID)
	if invocation == nil || invocation.identity != g.identity {
		return nil
	}
	invocation.recordUntil(t.now())
	switch kind {
	case blockingAutoReview:
		invocation.activeAutoReviews++
	case blockingUserWait:
		invocation.activeUserWaits++
	}
	return &BlockingGuard{kind: kind, identity: g.identity, timings: t, callID: g.callID}
}

// End records the end of the blocking interval. It is safe to call more than
// once.
func (b *BlockingGuard) End() {
	if b == nil {
		return
	}
	b.mu.Lock()
	if b.ended {
		b.mu.Unlock()
		return
	}
	b.ended = true
	b.mu.Unlock()
	b.timings.mu.Lock()
	defer b.timings.mu.Unlock()
	invocation := b.timings.uniqueWorkLocked(b.callID)
	if invocation == nil || invocation.identity != b.identity {
		return
	}
	invocation.recordUntil(b.timings.now())
	switch b.kind {
	case blockingAutoReview:
		if invocation.activeAutoReviews > 0 {
			invocation.activeAutoReviews--
		}
	case blockingUserWait:
		if invocation.activeUserWaits > 0 {
			invocation.activeUserWaits--
		}
	}
}

// ToolAdmission names the execution gate an invocation passed.
type ToolAdmission uint8

const (
	// AdmissionParallel depends on the last exclusive writer.
	AdmissionParallel ToolAdmission = iota
	// AdmissionExclusive depends on the latest completion.
	AdmissionExclusive
)

// Gate records runtime execution-lock handoffs. Admission guards must finish
// before their execution lock is released, so the next admitted invocation can
// inherit the completed dependency path.
type Gate struct {
	mu     sync.Mutex
	latest *CompletedWork
	writer *CompletedWork
	clock  func() time.Time
}

// NewGate returns an empty execution gate.
func NewGate() *Gate {
	return &Gate{clock: time.Now}
}

// AdmittedToolTiming finishes its invocation and publishes the completed path
// when Finish is called.
type AdmittedToolTiming struct {
	work      *ToolGuard
	gate      *Gate
	admission ToolAdmission
	once      sync.Once
}

// Admit records a dependency wait when the gate's previous work finished after
// this invocation started.
func (g *Gate) Admit(work *ToolGuard, admission ToolAdmission, startedAt time.Time) *AdmittedToolTiming {
	if g == nil || work == nil {
		return nil
	}
	g.mu.Lock()
	dependency := g.latest
	if admission == AdmissionParallel {
		dependency = g.writer
	}
	now := g.now()
	g.mu.Unlock()
	// Join takes the timings lock, so it runs after the gate lock is released to
	// keep a single lock order.
	if dependency != nil && dependency.EndedAt.After(startedAt) {
		work.timings.Join(work.callID, startedAt, now, dependency.Path)
	}
	return &AdmittedToolTiming{work: work, gate: g, admission: admission}
}

func (g *Gate) now() time.Time {
	if g == nil || g.clock == nil {
		return time.Now()
	}
	return g.clock()
}

// Finish completes the invocation and publishes its path to the gate.
func (a *AdmittedToolTiming) Finish() *CompletedWork {
	if a == nil {
		return nil
	}
	var completed *CompletedWork
	a.once.Do(func() {
		completed = a.work.Finish()
		if completed == nil {
			return
		}
		a.gate.mu.Lock()
		defer a.gate.mu.Unlock()
		if a.admission == AdmissionExclusive {
			a.gate.writer = completed
		}
		if a.gate.latest == nil || a.gate.latest.EndedAt.Before(completed.EndedAt) {
			a.gate.latest = completed
		}
	})
	return completed
}

func (i *invocation) recordUntil(now time.Time) {
	if i.path == nil {
		i.segmentStartedAt = now
		return
	}
	kind := criticalpath.WorkTools
	if i.activeUserWaits > 0 {
		kind = criticalpath.WorkUserWait
	} else if i.activeAutoReviews > 0 {
		kind = criticalpath.WorkAutoReview
	}
	work := i.path.Work(i.segmentStartedAt, now, kind)
	i.path = &work
	i.segmentStartedAt = now
}

func (i *invocation) join(start time.Time, end time.Time, dependency *criticalpath.Path) {
	i.recordUntil(start)
	if i.path != nil {
		if dependency != nil {
			work := i.path.Join(start, end, *dependency)
			i.path = &work
		} else {
			work := i.path.Work(start, end, criticalpath.WorkUnknown)
			i.path = &work
		}
	}
	i.segmentStartedAt = end
}
