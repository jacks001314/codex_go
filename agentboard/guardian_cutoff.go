package agentboard

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// GuardianHistoryPosition is the root history position Guardian intake has
// routed through. Rust guardian-v3 EvidenceCursor keeps the same three values,
// and a catch-up acknowledgement is a lower bound: later board writes stay
// visible without moving it back.
type GuardianHistoryPosition struct {
	ThreadID            string
	EndOrdinalExclusive uint64
	EndByteOffset       uint64
}

// Covers reports whether position reaches cutoff (Rust cutoff::covers). A
// position from another root is refused, and an ordinal that reaches the
// cutoff with a shorter byte offset is inconsistent rather than covered.
func (p GuardianHistoryPosition) Covers(cutoff GuardianHistoryPosition) (bool, error) {
	if strings.TrimSpace(p.ThreadID) != strings.TrimSpace(cutoff.ThreadID) {
		return false, errors.New("Guardian cutoff belongs to another root")
	}
	if p.EndOrdinalExclusive < cutoff.EndOrdinalExclusive {
		return false, nil
	}
	if p.EndByteOffset < cutoff.EndByteOffset {
		return false, errors.New("Guardian cutoff is inconsistent")
	}
	return true, nil
}

// GuardianReviewEvidence is the evidence cutoff captured for one approval
// review (Rust #63363 codex-rs/ext/guardian-v3/src/actor.rs). The first attempt
// uses the position captured when the action was bound; a retry captures a
// fresh position after core sampled a new authorization revision, and reuses
// the initial cutoff while the root revisions are unchanged.
type GuardianReviewEvidence struct {
	mu        sync.Mutex
	initial   GuardianHistoryPosition
	revisions uint64
	current   GuardianHistoryPosition
	attempts  int
}

// NewGuardianReviewEvidence binds the request-time history position and the
// root authorization revisions it was captured with.
func NewGuardianReviewEvidence(position GuardianHistoryPosition, revisions uint64) *GuardianReviewEvidence {
	position.ThreadID = strings.TrimSpace(position.ThreadID)
	return &GuardianReviewEvidence{initial: position, revisions: revisions, current: position}
}

// Position copies the cutoff for the next attempt.
func (e *GuardianReviewEvidence) Position() GuardianHistoryPosition {
	if e == nil {
		return GuardianHistoryPosition{}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.current
}

// Attempts is the number of attempt cutoffs handed out so far.
func (e *GuardianReviewEvidence) Attempts() int {
	if e == nil {
		return 0
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.attempts
}

// AttemptCutoff returns the cutoff for the next review attempt. The first call
// returns the request-time position without capturing. Later calls reuse that
// position while the root revisions still match the captured snapshot, and
// otherwise flush and capture a fresh position (Rust #63363: "Every later
// attempt flushes and captures again after core has sampled its new
// authorization revision"). A position from another root is refused.
func (e *GuardianReviewEvidence) AttemptCutoff(revisions uint64, capture func() (GuardianHistoryPosition, uint64, error)) (GuardianHistoryPosition, error) {
	if e == nil {
		return GuardianHistoryPosition{}, errors.New("Guardian review evidence is unavailable")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.attempts == 0 {
		e.attempts = 1
		return e.current, nil
	}
	e.attempts++
	if revisions == e.revisions {
		return e.initial, nil
	}
	return e.captureFreshLocked(capture)
}

// RequestCutoff returns the position a preparation must route through (Rust
// actor.rs request_through). The first preparation reuses the request-time
// position only while the root revisions still match what binding captured,
// because authorization can advance between binding and routing; a later
// attempt reuses the initial cutoff while authorization is unchanged and
// otherwise captures a fresh position. A position from another root is
// refused.
func (e *GuardianReviewEvidence) RequestCutoff(revisions uint64, capture func() (GuardianHistoryPosition, uint64, error)) (GuardianHistoryPosition, error) {
	if e == nil {
		return GuardianHistoryPosition{}, errors.New("Guardian review evidence is unavailable")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.attempts++
	if revisions == e.revisions {
		if e.attempts == 1 {
			return e.current, nil
		}
		return e.initial, nil
	}
	return e.captureFreshLocked(capture)
}

// captureFreshLocked flushes and captures a new position after core sampled a
// new authorization revision. The caller holds e.mu.
func (e *GuardianReviewEvidence) captureFreshLocked(capture func() (GuardianHistoryPosition, uint64, error)) (GuardianHistoryPosition, error) {
	if capture == nil {
		return GuardianHistoryPosition{}, errors.New("Guardian review evidence requires a fresh capture")
	}
	fresh, freshRevisions, err := capture()
	if err != nil {
		return GuardianHistoryPosition{}, err
	}
	fresh.ThreadID = strings.TrimSpace(fresh.ThreadID)
	if fresh.ThreadID != e.initial.ThreadID {
		return GuardianHistoryPosition{}, errors.New("Guardian cutoff belongs to another root")
	}
	e.revisions = freshRevisions
	e.current = fresh
	return fresh, nil
}

// GuardianRoutedEvidence is the root's routing watermark: the housekeeper
// advances it after publishing (Rust LiveDelivery::routed / Capture::routed_through),
// and a catch-up request waits on it instead of polling the board.
type GuardianRoutedEvidence struct {
	root string

	mu     sync.Mutex
	routed GuardianHistoryPosition
	wake   chan struct{}
	// stopped reports that no further routing can arrive, so waiters fail
	// instead of blocking forever (Rust "Guardian housekeeper stopped").
	stopped bool
	stopErr error
}

// NewGuardianRoutedEvidence opens the watermark at the intake cursor.
func NewGuardianRoutedEvidence(start GuardianHistoryPosition) (*GuardianRoutedEvidence, error) {
	root := strings.TrimSpace(start.ThreadID)
	if root == "" {
		return nil, errors.New("Guardian routing watermark requires a root thread")
	}
	start.ThreadID = root
	return &GuardianRoutedEvidence{root: root, routed: start, wake: make(chan struct{})}, nil
}

// Root is the board identity this watermark belongs to.
func (r *GuardianRoutedEvidence) Root() string {
	if r == nil {
		return ""
	}
	return r.root
}

// Position copies the last routed position.
func (r *GuardianRoutedEvidence) Position() GuardianHistoryPosition {
	if r == nil {
		return GuardianHistoryPosition{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.routed
}

// RoutedThrough advances the watermark after the sole writer published every
// delivery through position. Positions for another root are refused so a
// catch-up cannot be acknowledged against the wrong history.
func (r *GuardianRoutedEvidence) RoutedThrough(position GuardianHistoryPosition) error {
	if r == nil {
		return errors.New("Guardian routing watermark is unavailable")
	}
	position.ThreadID = strings.TrimSpace(position.ThreadID)
	if position.ThreadID != r.root {
		return errors.New("Guardian cutoff belongs to another root")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return errors.New("Guardian housekeeper stopped")
	}
	if position == r.routed {
		return nil
	}
	r.routed = position
	r.publishLocked()
	return nil
}

// Stop ends the watermark: pending waiters fail with err and later advances are
// refused. A nil err reports that the housekeeper stopped.
func (r *GuardianRoutedEvidence) Stop(err error) {
	if r == nil {
		return
	}
	if err == nil {
		err = errors.New("Guardian housekeeper stopped")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return
	}
	r.stopped = true
	r.stopErr = err
	r.publishLocked()
}

// WaitUntilRouted returns once the watermark reaches target, which makes it the
// acknowledgement a catch-up request resolves with (Rust
// cutoff::wait_until_routed). It observes the covered position rather than
// waiting for an exact equality, so a later write does not unblock-ack an
// earlier request.
func (r *GuardianRoutedEvidence) WaitUntilRouted(ctx context.Context, target GuardianHistoryPosition) (GuardianHistoryPosition, error) {
	if r == nil {
		return GuardianHistoryPosition{}, errors.New("Guardian routing watermark is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	target.ThreadID = strings.TrimSpace(target.ThreadID)
	if target.ThreadID != r.root {
		return GuardianHistoryPosition{}, errors.New("Guardian cutoff belongs to another root")
	}
	for {
		r.mu.Lock()
		current := r.routed
		wake := r.wake
		stopped, stopErr := r.stopped, r.stopErr
		r.mu.Unlock()
		if stopped {
			return current, stopErr
		}
		covered, err := current.Covers(target)
		if err != nil {
			return current, err
		}
		if covered {
			return target, nil
		}
		select {
		case <-ctx.Done():
			return current, ctx.Err()
		case <-wake:
		}
	}
}

func (r *GuardianRoutedEvidence) publishLocked() {
	if r.wake == nil {
		r.wake = make(chan struct{})
	}
	close(r.wake)
	r.wake = make(chan struct{})
}

// GuardianHistoryPositionString renders a position for diagnostics without
// exposing a mutable copy.
func GuardianHistoryPositionString(position GuardianHistoryPosition) string {
	return fmt.Sprintf("%s:%d/%d", strings.TrimSpace(position.ThreadID), position.EndOrdinalExclusive, position.EndByteOffset)
}
