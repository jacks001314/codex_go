package agentboard

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func guardianCutoffTestPosition(ordinal uint64, offset uint64) GuardianHistoryPosition {
	return GuardianHistoryPosition{ThreadID: "root", EndOrdinalExclusive: ordinal, EndByteOffset: offset}
}

// TestGuardianPreparedReviewWaitsForRoutingLikeRust mirrors Rust #63363: the
// first attempt routes through the request-time position, and preparation does
// not return until the watermark reaches it.
func TestGuardianPreparedReviewWaitsForRoutingLikeRust(t *testing.T) {
	initial := guardianCutoffTestPosition(2, 20)
	evidence := NewGuardianReviewEvidence(initial, 5)
	routed, err := NewGuardianRoutedEvidence(guardianCutoffTestPosition(1, 10))
	if err != nil {
		t.Fatalf("NewGuardianRoutedEvidence() error = %v", err)
	}
	prepared, err := NewGuardianPreparedReview(evidence, routed)
	if err != nil {
		t.Fatalf("NewGuardianPreparedReview() error = %v", err)
	}
	var captures atomic.Int32
	capture := func() (GuardianHistoryPosition, uint64, error) {
		captures.Add(1)
		return guardianCutoffTestPosition(9, 90), 9, nil
	}
	done := make(chan struct {
		position GuardianHistoryPosition
		err      error
	}, 1)
	go func() {
		position, err := prepared.Prepare(context.Background(), 5, capture)
		done <- struct {
			position GuardianHistoryPosition
			err      error
		}{position, err}
	}()
	select {
	case result := <-done:
		t.Fatalf("Prepare() returned before routing reached the cutoff: %#v, %v", result.position, result.err)
	case <-time.After(50 * time.Millisecond):
	}
	if err := routed.RoutedThrough(initial); err != nil {
		t.Fatalf("RoutedThrough() error = %v", err)
	}
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("Prepare() error = %v", result.err)
		}
		if result.position != initial {
			t.Fatalf("Prepare() = %#v, want the request-time cutoff %#v", result.position, initial)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Prepare() did not return after routing reached the cutoff")
	}
	if got := captures.Load(); got != 0 {
		t.Fatalf("captured %d fresh positions, want the request-time one reused", got)
	}
}

// TestGuardianPreparedReviewCapturesFreshAuthorizationLikeRust mirrors Rust
// #63363: authorization may advance between binding and routing, so a
// preparation that sees new revisions captures a fresh position and routes
// through it.
func TestGuardianPreparedReviewCapturesFreshAuthorizationLikeRust(t *testing.T) {
	initial := guardianCutoffTestPosition(2, 20)
	fresh := guardianCutoffTestPosition(6, 60)
	evidence := NewGuardianReviewEvidence(initial, 5)
	routed, err := NewGuardianRoutedEvidence(guardianCutoffTestPosition(1, 10))
	if err != nil {
		t.Fatalf("NewGuardianRoutedEvidence() error = %v", err)
	}
	prepared, err := NewGuardianPreparedReview(evidence, routed)
	if err != nil {
		t.Fatalf("NewGuardianPreparedReview() error = %v", err)
	}
	var captures atomic.Int32
	capture := func() (GuardianHistoryPosition, uint64, error) {
		captures.Add(1)
		return fresh, 6, nil
	}
	done := make(chan GuardianHistoryPosition, 1)
	failures := make(chan error, 1)
	go func() {
		position, err := prepared.Prepare(context.Background(), 6, capture)
		if err != nil {
			failures <- err
			return
		}
		done <- position
	}()
	select {
	case err := <-failures:
		t.Fatalf("Prepare() error = %v", err)
	case position := <-done:
		t.Fatalf("Prepare() returned %#v before the fresh cutoff was routed", position)
	case <-time.After(50 * time.Millisecond):
	}
	if got := captures.Load(); got != 1 {
		t.Fatalf("captures = %d, want one fresh capture", got)
	}
	if err := routed.RoutedThrough(fresh); err != nil {
		t.Fatalf("RoutedThrough() error = %v", err)
	}
	select {
	case position := <-done:
		if position != fresh {
			t.Fatalf("Prepare() = %#v, want the fresh cutoff %#v", position, fresh)
		}
	case err := <-failures:
		t.Fatalf("Prepare() error = %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Prepare() did not return after the fresh cutoff was routed")
	}

	// A retry whose authorization is unchanged reuses the bound cutoff.
	retry, err := prepared.Prepare(context.Background(), 6, capture)
	if err != nil {
		t.Fatalf("retry Prepare() error = %v", err)
	}
	if retry != initial || captures.Load() != 1 {
		t.Fatalf("retry Prepare() = %#v (captures %d), want the initial cutoff reused", retry, captures.Load())
	}
}

// TestGuardianPreparedReviewReportsCancellationAndStoppedRoutingLikeRust
// separates a cancelled review from evidence that routing can no longer
// deliver.
func TestGuardianPreparedReviewReportsCancellationAndStoppedRoutingLikeRust(t *testing.T) {
	evidence := NewGuardianReviewEvidence(guardianCutoffTestPosition(2, 20), 5)
	routed, err := NewGuardianRoutedEvidence(guardianCutoffTestPosition(1, 10))
	if err != nil {
		t.Fatalf("NewGuardianRoutedEvidence() error = %v", err)
	}
	prepared, err := NewGuardianPreparedReview(evidence, routed)
	if err != nil {
		t.Fatalf("NewGuardianPreparedReview() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancelled := make(chan error, 1)
	go func() {
		_, err := prepared.Prepare(ctx, 5, nil)
		cancelled <- err
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-cancelled:
		if !errors.Is(err, ErrGuardianReviewCancelled) {
			t.Fatalf("cancelled Prepare() error = %v, want ErrGuardianReviewCancelled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Prepare() ignored cancellation")
	}

	stoppedEvidence := NewGuardianReviewEvidence(guardianCutoffTestPosition(3, 30), 5)
	stoppedRouted, err := NewGuardianRoutedEvidence(guardianCutoffTestPosition(1, 10))
	if err != nil {
		t.Fatalf("NewGuardianRoutedEvidence() error = %v", err)
	}
	stopped, err := NewGuardianPreparedReview(stoppedEvidence, stoppedRouted)
	if err != nil {
		t.Fatalf("NewGuardianPreparedReview() error = %v", err)
	}
	stoppedRouted.Stop(nil)
	if _, err := stopped.Prepare(context.Background(), 5, nil); !errors.Is(err, ErrGuardianEvidenceUnavailable) {
		t.Fatalf("stopped Prepare() error = %v, want ErrGuardianEvidenceUnavailable", err)
	}
}

// TestGuardianPreparedReviewRejectsForeignRootLikeRust keeps a review bound to
// evidence and routing from different roots from being acknowledged.
func TestGuardianPreparedReviewRejectsForeignRootLikeRust(t *testing.T) {
	evidence := NewGuardianReviewEvidence(guardianCutoffTestPosition(2, 20), 5)
	foreign, err := NewGuardianRoutedEvidence(GuardianHistoryPosition{ThreadID: "other", EndOrdinalExclusive: 1, EndByteOffset: 10})
	if err != nil {
		t.Fatalf("NewGuardianRoutedEvidence() error = %v", err)
	}
	if _, err := NewGuardianPreparedReview(evidence, foreign); err == nil {
		t.Fatal("NewGuardianPreparedReview() accepted another root's watermark")
	}
	if _, err := NewGuardianPreparedReview(nil, foreign); err == nil {
		t.Fatal("NewGuardianPreparedReview() accepted missing evidence")
	}
	var missing *GuardianPreparedReview
	if _, err := missing.Prepare(context.Background(), 5, nil); !errors.Is(err, ErrGuardianEvidenceUnavailable) {
		t.Fatalf("nil Prepare() error = %v, want ErrGuardianEvidenceUnavailable", err)
	}
}
