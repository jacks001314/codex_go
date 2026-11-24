package agentboard

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestGuardianRoutedEvidenceAcknowledgesCatchUpLikeRust mirrors Rust
// guardian-v3 cutoff.rs: a catch-up resolves only after the housekeeper routed
// through the requested position, and it is a lower bound, so later writes stay
// visible without moving the acknowledgement back.
func TestGuardianRoutedEvidenceAcknowledgesCatchUpLikeRust(t *testing.T) {
	start := GuardianHistoryPosition{ThreadID: "root", EndOrdinalExclusive: 1}
	routed, err := NewGuardianRoutedEvidence(start)
	if err != nil {
		t.Fatalf("NewGuardianRoutedEvidence() error = %v", err)
	}
	target := GuardianHistoryPosition{ThreadID: "root", EndOrdinalExclusive: 3, EndByteOffset: 500}
	type result struct {
		position GuardianHistoryPosition
		err      error
	}
	done := make(chan result, 1)
	go func() {
		position, err := routed.WaitUntilRouted(context.Background(), target)
		done <- result{position: position, err: err}
	}()

	// A shorter ordinal leaves the request uncovered while the byte offset fits.
	if err := routed.RoutedThrough(GuardianHistoryPosition{ThreadID: "root", EndOrdinalExclusive: 2, EndByteOffset: 500}); err != nil {
		t.Fatalf("RoutedThrough(partial) error = %v", err)
	}
	select {
	case got := <-done:
		t.Fatalf("catch-up resolved before covering the request: %#v", got)
	case <-time.After(50 * time.Millisecond):
	}
	if err := routed.RoutedThrough(target); err != nil {
		t.Fatalf("RoutedThrough(target) error = %v", err)
	}
	select {
	case got := <-done:
		if got.err != nil || got.position != target {
			t.Fatalf("catch-up result = %#v, want %#v", got, target)
		}
	case <-time.After(time.Second):
		t.Fatal("catch-up did not resolve after routing reached it")
	}

	// A later write advances the watermark and still satisfies the older request.
	later := GuardianHistoryPosition{ThreadID: "root", EndOrdinalExclusive: 4, EndByteOffset: 900}
	if err := routed.RoutedThrough(later); err != nil {
		t.Fatalf("RoutedThrough(later) error = %v", err)
	}
	if got := routed.Position(); got != later {
		t.Fatalf("watermark = %#v, want %#v", got, later)
	}
	if got, err := routed.WaitUntilRouted(context.Background(), target); err != nil || got != target {
		t.Fatalf("acknowledged position = %#v/%v", got, err)
	}
}

// TestGuardianRoutedEvidenceRejectsOtherRootLikeRust keeps a watermark bound to
// one root thread, like Rust's covers() check.
func TestGuardianRoutedEvidenceRejectsOtherRootLikeRust(t *testing.T) {
	routed, err := NewGuardianRoutedEvidence(GuardianHistoryPosition{ThreadID: "root"})
	if err != nil {
		t.Fatalf("NewGuardianRoutedEvidence() error = %v", err)
	}
	other := GuardianHistoryPosition{ThreadID: "other", EndOrdinalExclusive: 1}
	if err := routed.RoutedThrough(other); err == nil {
		t.Fatal("RoutedThrough() accepted another root")
	}
	if _, err := routed.WaitUntilRouted(context.Background(), other); err == nil {
		t.Fatal("WaitUntilRouted() accepted another root")
	}
	if _, err := (GuardianHistoryPosition{ThreadID: "root"}).Covers(other); err == nil {
		t.Fatal("Covers() accepted a cutoff from another root")
	}
}

// TestGuardianRoutedEvidenceInconsistentCutoffLikeRust refuses a position whose
// ordinal reaches the cutoff while its byte offset falls short.
func TestGuardianRoutedEvidenceInconsistentCutoffLikeRust(t *testing.T) {
	inconsistent := GuardianHistoryPosition{ThreadID: "root", EndOrdinalExclusive: 3, EndByteOffset: 100}
	if _, err := inconsistent.Covers(GuardianHistoryPosition{ThreadID: "root", EndOrdinalExclusive: 2, EndByteOffset: 500}); err == nil {
		t.Fatal("Covers() accepted an inconsistent cutoff")
	}
	routed, err := NewGuardianRoutedEvidence(inconsistent)
	if err != nil {
		t.Fatalf("NewGuardianRoutedEvidence() error = %v", err)
	}
	// Rust's covers() reports the inconsistent cut as an error, so the catch-up
	// fails immediately instead of waiting for a position that cannot cover it.
	position, err := routed.WaitUntilRouted(context.Background(), GuardianHistoryPosition{ThreadID: "root", EndOrdinalExclusive: 3, EndByteOffset: 500})
	if err == nil || err.Error() != "Guardian cutoff is inconsistent" {
		t.Fatalf("WaitUntilRouted() error = %v, want the inconsistent cutoff", err)
	}
	if position != inconsistent {
		t.Fatalf("position after the refusal = %#v", position)
	}
}

// TestGuardianRoutedEvidenceStopReleasesWaitersLikeRust mirrors the writer
// shutting down: a pending catch-up fails instead of blocking forever.
func TestGuardianRoutedEvidenceStopReleasesWaitersLikeRust(t *testing.T) {
	routed, err := NewGuardianRoutedEvidence(GuardianHistoryPosition{ThreadID: "root"})
	if err != nil {
		t.Fatalf("NewGuardianRoutedEvidence() error = %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := routed.WaitUntilRouted(context.Background(), GuardianHistoryPosition{ThreadID: "root", EndOrdinalExclusive: 2})
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	stopErr := errors.New("guardian housekeeper stopped")
	routed.Stop(stopErr)
	select {
	case err := <-done:
		if !errors.Is(err, stopErr) {
			t.Fatalf("waiter error = %v, want %v", err, stopErr)
		}
	case <-time.After(time.Second):
		t.Fatal("waiter was not released by Stop")
	}
	if err := routed.RoutedThrough(GuardianHistoryPosition{ThreadID: "root", EndOrdinalExclusive: 3}); err == nil {
		t.Fatal("RoutedThrough() accepted an advance after Stop")
	}
}
