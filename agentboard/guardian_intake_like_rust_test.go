package agentboard

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// TestGuardianIntakeCatchUpWaitsForRoutingLikeRust mirrors Rust #63361: a
// resuming root catches up through the durable tail it read, and the
// acknowledgement arrives only once routing reached that position.
func TestGuardianIntakeCatchUpWaitsForRoutingLikeRust(t *testing.T) {
	intakes := NewGuardianIntakes()
	intake, err := intakes.ForRoot("root", guardianCutoffTestPosition(2, 20))
	if err != nil {
		t.Fatalf("ForRoot() error = %v", err)
	}
	tail := guardianCutoffTestPosition(5, 50)
	var mu sync.Mutex
	var enqueued []GuardianHistoryPosition
	done := make(chan GuardianHistoryPosition, 1)
	failures := make(chan error, 1)
	go func() {
		ack, err := intake.CatchUp(context.Background(),
			func() (GuardianHistoryPosition, error) { return tail, nil },
			func(position GuardianHistoryPosition) error {
				mu.Lock()
				enqueued = append(enqueued, position)
				mu.Unlock()
				return nil
			})
		if err != nil {
			failures <- err
			return
		}
		done <- ack
	}()
	select {
	case ack := <-done:
		t.Fatalf("CatchUp() acknowledged %#v before routing reached the tail", ack)
	case err := <-failures:
		t.Fatalf("CatchUp() error = %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	mu.Lock()
	got := append([]GuardianHistoryPosition(nil), enqueued...)
	mu.Unlock()
	if len(got) != 1 || got[0] != tail {
		t.Fatalf("enqueued = %#v, want the durable tail %#v", got, tail)
	}
	if err := intake.Routed().RoutedThrough(tail); err != nil {
		t.Fatalf("RoutedThrough() error = %v", err)
	}
	select {
	case ack := <-done:
		if ack != tail {
			t.Fatalf("CatchUp() = %#v, want %#v", ack, tail)
		}
	case err := <-failures:
		t.Fatalf("CatchUp() error = %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("CatchUp() did not acknowledge after routing reached the tail")
	}
}

// TestGuardianIntakeSurvivesReloadLikeRust mirrors Rust #63361: a reload reuses
// the root's routing progress, while a retired intake is replaced and a review
// prepared against the old one is refused.
func TestGuardianIntakeSurvivesReloadLikeRust(t *testing.T) {
	intakes := NewGuardianIntakes()
	first, err := intakes.ForRoot("root", guardianCutoffTestPosition(1, 10))
	if err != nil {
		t.Fatalf("ForRoot() error = %v", err)
	}
	reloaded, err := intakes.ForRoot("root", guardianCutoffTestPosition(9, 90))
	if err != nil {
		t.Fatalf("reload ForRoot() error = %v", err)
	}
	if reloaded != first || reloaded.Routed() != first.Routed() {
		t.Fatal("reload opened a fresh intake instead of resuming routing progress")
	}
	if err := first.Routed().RoutedThrough(guardianCutoffTestPosition(3, 30)); err != nil {
		t.Fatalf("RoutedThrough() error = %v", err)
	}

	initial := guardianCutoffTestPosition(3, 30)
	evidence := NewGuardianReviewEvidence(initial, 5)
	prepared, err := NewGuardianPreparedReview(evidence, first.Routed())
	if err != nil {
		t.Fatalf("NewGuardianPreparedReview() error = %v", err)
	}
	position, err := prepared.PrepareForIntake(context.Background(), first, 5, nil)
	if err != nil {
		t.Fatalf("PrepareForIntake() error = %v", err)
	}
	if position != initial {
		t.Fatalf("PrepareForIntake() = %#v, want the bound cutoff", position)
	}

	intakes.Retire("root")
	reopened, err := intakes.ForRoot("root", guardianCutoffTestPosition(1, 10))
	if err != nil {
		t.Fatalf("reopen ForRoot() error = %v", err)
	}
	if reopened == first || reopened.Routed() == first.Routed() {
		t.Fatal("Retire() kept the retired intake")
	}
	if !first.Delivery().Closed() {
		t.Fatal("Retire() left the retired delivery stream open")
	}
	_, err = prepared.PrepareForIntake(context.Background(), reopened, 5, nil)
	if !errors.Is(err, ErrGuardianEvidenceUnavailable) {
		t.Fatalf("PrepareForIntake() against a replacement intake = %v, want ErrGuardianEvidenceUnavailable", err)
	}
}

// TestGuardianIntakeCatchUpRejectsForeignTailLikeRust keeps a catch-up from
// acknowledging another root's durable tail, and reports a stopped intake
// instead of waiting forever.
func TestGuardianIntakeCatchUpRejectsForeignTailLikeRust(t *testing.T) {
	intakes := NewGuardianIntakes()
	intake, err := intakes.ForRoot("root", guardianCutoffTestPosition(1, 10))
	if err != nil {
		t.Fatalf("ForRoot() error = %v", err)
	}
	foreign := func() (GuardianHistoryPosition, error) {
		return GuardianHistoryPosition{ThreadID: "other", EndOrdinalExclusive: 4, EndByteOffset: 40}, nil
	}
	if _, err := intake.CatchUp(context.Background(), foreign, nil); err == nil {
		t.Fatal("CatchUp() accepted another root's tail")
	}
	intake.Routed().Stop(nil)
	tail := func() (GuardianHistoryPosition, error) { return guardianCutoffTestPosition(4, 40), nil }
	if _, err := intake.CatchUp(context.Background(), tail, nil); err == nil {
		t.Fatal("CatchUp() waited on a stopped intake")
	}
}
