package realtime

// Mirrors Rust #62288: a handoff claims its session's transcript so overlapping
// handoffs cannot consume the same speech twice, a definite rejection releases
// the claim, and the receiving turn's session binding consumes it.

import "testing"

func TestRealtimeHandoffReservationLikeRust(t *testing.T) {
	history := &RealtimeHistoryState{}
	if history.ReserveHandoff("h1") {
		t.Fatal("a handoff reserved speech without an active session")
	}
	history.StartSession("session-1")
	if !history.ReserveHandoff("h1") {
		t.Fatal("first handoff did not claim the session")
	}
	if history.ReserveHandoff("h1") {
		t.Fatal("a duplicate handoff claimed the session twice")
	}
	if history.ReserveHandoff("h2") {
		t.Fatal("an overlapping handoff claimed speech already reserved")
	}
	if !history.HandoffReserved("h1") {
		t.Fatal("reservation disappeared before the turn bound it")
	}

	// A definite rejection frees the speech for the next input.
	if !history.ReleaseHandoff("h1") {
		t.Fatal("release reported no claim")
	}
	if history.ReleaseHandoff("h1") {
		t.Fatal("release reported a claim twice")
	}
	if !history.ReserveHandoff("h2") {
		t.Fatal("released speech was not available to the next handoff")
	}

	// The receiving turn consumes the claim, so a later handoff must reserve again.
	history.BindTurnSession("turn-1")
	if history.HandoffReserved("h2") {
		t.Fatal("turn binding did not consume the claim")
	}
}
