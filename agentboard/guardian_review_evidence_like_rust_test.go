package agentboard

import (
	"errors"
	"testing"
)

// TestGuardianReviewEvidenceCutoffLikeRust mirrors Rust #63363: the first
// attempt uses the request-time position, a retry reuses it while the root
// revisions are unchanged, and a changed authorization captures a fresh one.
func TestGuardianReviewEvidenceCutoffLikeRust(t *testing.T) {
	initial := GuardianHistoryPosition{ThreadID: "root", EndOrdinalExclusive: 4, EndByteOffset: 40}
	evidence := NewGuardianReviewEvidence(initial, 7)
	captureCalls := 0
	capture := func() (GuardianHistoryPosition, uint64, error) {
		captureCalls++
		return GuardianHistoryPosition{ThreadID: "root", EndOrdinalExclusive: 9, EndByteOffset: 90}, 8, nil
	}

	first, err := evidence.AttemptCutoff(7, capture)
	if err != nil {
		t.Fatalf("first AttemptCutoff() error = %v", err)
	}
	if first != initial || captureCalls != 0 {
		t.Fatalf("first cutoff = %#v (capture calls %d), want the request-time position", first, captureCalls)
	}

	retry, err := evidence.AttemptCutoff(7, capture)
	if err != nil {
		t.Fatalf("retry AttemptCutoff() error = %v", err)
	}
	if retry != initial || captureCalls != 0 {
		t.Fatalf("unchanged-revision retry = %#v (capture calls %d), want the initial cutoff", retry, captureCalls)
	}

	fresh, err := evidence.AttemptCutoff(8, capture)
	if err != nil {
		t.Fatalf("changed-authorization AttemptCutoff() error = %v", err)
	}
	if captureCalls != 1 || fresh.EndOrdinalExclusive != 9 {
		t.Fatalf("changed-authorization cutoff = %#v (capture calls %d)", fresh, captureCalls)
	}
	if got := evidence.Position(); got != fresh {
		t.Fatalf("Position() = %#v, want the fresh cutoff", got)
	}
	if got := evidence.Attempts(); got != 3 {
		t.Fatalf("Attempts() = %d, want 3", got)
	}
}

// TestGuardianReviewEvidenceRejectsForeignRootLikeRust keeps a fresh capture
// from acknowledging another root's history.
func TestGuardianReviewEvidenceRejectsForeignRootLikeRust(t *testing.T) {
	evidence := NewGuardianReviewEvidence(GuardianHistoryPosition{ThreadID: "root", EndOrdinalExclusive: 1, EndByteOffset: 10}, 1)
	if _, err := evidence.AttemptCutoff(1, nil); err != nil {
		t.Fatalf("first AttemptCutoff() error = %v", err)
	}
	foreign := func() (GuardianHistoryPosition, uint64, error) {
		return GuardianHistoryPosition{ThreadID: "other", EndOrdinalExclusive: 2, EndByteOffset: 20}, 2, nil
	}
	if _, err := evidence.AttemptCutoff(2, foreign); err == nil {
		t.Fatal("AttemptCutoff() accepted another root's fresh position")
	}
	failing := func() (GuardianHistoryPosition, uint64, error) {
		return GuardianHistoryPosition{}, 0, errors.New("history unavailable")
	}
	if _, err := evidence.AttemptCutoff(3, failing); err == nil {
		t.Fatal("AttemptCutoff() hid a capture failure")
	}
	noCapture, err := evidence.AttemptCutoff(3, nil)
	if err == nil || noCapture != (GuardianHistoryPosition{}) {
		t.Fatalf("AttemptCutoff() without a capture = %#v, %v", noCapture, err)
	}
}
