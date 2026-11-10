package appserver

import "testing"

// TestTurnCompletionCarriesOnlyItsOwnErrorLikeRust pins Rust #60434's intent in
// the Go app-server: Rust had to reset ThreadState.turn_summary on TurnStarted
// because its completion event read an accumulated summary, so an error recorded
// before a turn could make a successful turn report failed. Go builds each
// completion from that turn alone, and this test keeps it that way: a completed
// turn carries no error even when the previous turn failed.
func TestTurnCompletionCarriesOnlyItsOwnErrorLikeRust(t *testing.T) {
	startedAt := int64(42)
	completedAt := int64(50)
	durationMS := int64(8)

	failed := completedTurnNotificationTurn(
		"turn-failed", "turn-failed", TurnStatusFailed,
		&TurnError{Message: "conversation is not running"},
		&startedAt, &completedAt, &durationMS,
	)
	if failed.Status != TurnStatusFailed || failed.Error == nil || failed.Error.Message != "conversation is not running" {
		t.Fatalf("failed turn = %#v, want its own error", failed)
	}

	next := completedTurnNotificationTurn(
		"turn-next", "turn-next", TurnStatusCompleted, nil,
		&startedAt, &completedAt, &durationMS,
	)
	if next.Status != TurnStatusCompleted {
		t.Fatalf("completed turn status = %q, want completed", next.Status)
	}
	if next.Error != nil {
		t.Fatalf("completed turn inherited an error: %#v", next.Error)
	}
	if next.ID != "turn-next" || next.StartedAt == nil || *next.StartedAt != startedAt {
		t.Fatalf("completed turn = %#v, want its own identity and start time", next)
	}
}
