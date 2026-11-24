package agentboard

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// GuardianIntake is one root thread's evidence intake: the live delivery
// stream and the routing watermark. A reloaded root resumes this intake and
// catches up from the last completed original instead of restarting at the end
// of the history (Rust #63361 RootEvidence).
type GuardianIntake struct {
	root     string
	delivery *GuardianLiveDelivery
	routed   *GuardianRoutedEvidence
}

// Root is the board identity this intake belongs to.
func (i *GuardianIntake) Root() string {
	if i == nil {
		return ""
	}
	return i.root
}

// Delivery is the live inbox stream shared by the root's child reviewers.
func (i *GuardianIntake) Delivery() *GuardianLiveDelivery {
	if i == nil {
		return nil
	}
	return i.delivery
}

// Routed is the routing watermark a prepared review waits on.
func (i *GuardianIntake) Routed() *GuardianRoutedEvidence {
	if i == nil {
		return nil
	}
	return i.routed
}

// CatchUp submits routing work through the current durable tail and returns the
// acknowledgement once the watermark covers it (Rust
// RootEvidence::request_catch_up). tail flushes and reads the durable tail;
// enqueue hands the housekeeper the position it must route through.
func (i *GuardianIntake) CatchUp(ctx context.Context, tail func() (GuardianHistoryPosition, error), enqueue func(GuardianHistoryPosition) error) (GuardianHistoryPosition, error) {
	if i == nil || i.routed == nil {
		return GuardianHistoryPosition{}, errors.New("Guardian intake is unavailable")
	}
	if tail == nil {
		return GuardianHistoryPosition{}, errors.New("Guardian catch-up requires the durable tail")
	}
	target, err := tail()
	if err != nil {
		return GuardianHistoryPosition{}, err
	}
	target.ThreadID = strings.TrimSpace(target.ThreadID)
	if target.ThreadID != i.root {
		return GuardianHistoryPosition{}, errors.New("Guardian cutoff belongs to another root")
	}
	if enqueue != nil {
		if err := enqueue(target); err != nil {
			return GuardianHistoryPosition{}, err
		}
	}
	ack, err := i.routed.WaitUntilRouted(ctx, target)
	if err != nil {
		return ack, err
	}
	return ack, nil
}

// GuardianIntakes keeps one intake per root thread so an unload that leaves a
// child reviewer alive does not reset routing progress, subscriptions or queued
// evidence. Retiring a root closes its stream and stops its watermark, so the
// next load starts from a fresh intake.
type GuardianIntakes struct {
	mu     sync.Mutex
	byRoot map[string]*GuardianIntake
}

// NewGuardianIntakes creates an empty intake registry.
func NewGuardianIntakes() *GuardianIntakes {
	return &GuardianIntakes{byRoot: map[string]*GuardianIntake{}}
}

// ForRoot returns the root's intake, opening it at start on first use. A reload
// reuses the same intake, including its watermark and inboxes.
func (r *GuardianIntakes) ForRoot(root string, start GuardianHistoryPosition) (*GuardianIntake, error) {
	if r == nil {
		return nil, errors.New("Guardian intakes are unavailable")
	}
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("Guardian intake requires a root thread")
	}
	start.ThreadID = root
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.byRoot == nil {
		r.byRoot = map[string]*GuardianIntake{}
	}
	if intake, ok := r.byRoot[root]; ok {
		return intake, nil
	}
	routed, err := NewGuardianRoutedEvidence(start)
	if err != nil {
		return nil, err
	}
	intake := &GuardianIntake{root: root, delivery: NewGuardianLiveDelivery(root), routed: routed}
	r.byRoot[root] = intake
	return intake, nil
}

// Existing returns the root's intake without opening one.
func (r *GuardianIntakes) Existing(root string) (*GuardianIntake, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	intake, ok := r.byRoot[strings.TrimSpace(root)]
	return intake, ok
}

// Retire closes the root's stream, stops its watermark and forgets the intake.
func (r *GuardianIntakes) Retire(root string) {
	if r == nil {
		return
	}
	root = strings.TrimSpace(root)
	r.mu.Lock()
	intake := r.byRoot[root]
	delete(r.byRoot, root)
	r.mu.Unlock()
	intake.retire()
}

// RetireAll retires every root's intake, e.g. when the host shuts down.
func (r *GuardianIntakes) RetireAll() {
	if r == nil {
		return
	}
	r.mu.Lock()
	intakes := r.byRoot
	r.byRoot = map[string]*GuardianIntake{}
	r.mu.Unlock()
	for _, intake := range intakes {
		intake.retire()
	}
}

func (i *GuardianIntake) retire() {
	if i == nil {
		return
	}
	i.delivery.CloseAll()
	i.routed.Stop(nil)
}

// PrepareForIntake prepares a review only while its routing watermark is still
// the root's current intake, because a fresh delivery stream replaces the
// inboxes the evidence would have been routed to (Rust #63361: review
// preparation checks that catch-up completed against the current root intake).
func (p *GuardianPreparedReview) PrepareForIntake(ctx context.Context, intake *GuardianIntake, revisions uint64, capture func() (GuardianHistoryPosition, uint64, error)) (GuardianHistoryPosition, error) {
	if p == nil || intake == nil || intake.routed != p.routed {
		return GuardianHistoryPosition{}, fmt.Errorf("%w: Guardian intake was replaced", ErrGuardianEvidenceUnavailable)
	}
	return p.Prepare(ctx, revisions, capture)
}
