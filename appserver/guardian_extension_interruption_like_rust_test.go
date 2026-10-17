package appserver

import (
	"context"
	"testing"
	"time"

	"codex_go/turn"
)

// Mirrors Rust #45677's `Session::interrupt_turn_with_warning` behavior: an
// extension-requested interruption of the still-active turn cancels it and
// emits the interrupted thread-idle lifecycle transition.
func TestGuardianCircuitBreakerInterruptionEmitsIdleStatusLikeRust(t *testing.T) {
	router, _, childID := guardianNotificationTestRouter(t, true)
	threadID := string(childID)
	turnID := "turn-child"
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	router.requireThreadStatus().NoteTurnStarted(threadID)
	if err := router.threads.ReserveTurn(threadID); err != nil {
		t.Fatalf("ReserveTurn() error = %v", err)
	}
	if err := router.threads.RegisterTurn(threadID, turnID, cancel, time.Now().UTC().UnixMilli(), &turn.TurnStartParams{ThreadID: threadID}); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}
	if got := router.requireThreadStatus().LoadedStatusForThread(threadID); got.Type != ActiveStatus().Type {
		t.Fatalf("status before interruption = %#v, want active", got)
	}

	router.interruptTurnForGuardianCircuitBreaker(threadID, turnID, "The action was denied three times.")

	if got := router.requireThreadStatus().LoadedStatusForThread(threadID); got.Type != IdleStatus().Type {
		t.Fatalf("status after interruption = %#v, want idle", got)
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("the active turn was not cancelled")
	}
}
