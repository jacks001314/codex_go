package appserver

import (
	"errors"
	"testing"
)

// Rust parity: codex-rs/app-server/src/turn_admission.rs at #43959. Draining
// closes admission for new turns and automatic continuations but leaves active
// turns running until shutdown takes them for interruption.
func TestBeginDrainingRejectsNewTurnsButKeepsActiveTurnsLikeRust(t *testing.T) {
	manager := NewThreadManager(NewThreadStatusManager())
	if err := manager.ReserveTurn("thread-1"); err != nil {
		t.Fatalf("ReserveTurn() error = %v", err)
	}
	activeCancelled := false
	if err := manager.RegisterTurn("thread-1", "turn-1", func() { activeCancelled = true }, 0, nil); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}

	manager.BeginDraining()
	if err := manager.ReserveTurn("thread-2"); !errors.Is(err, ErrServerDraining) {
		t.Fatalf("ReserveTurn() during drain error = %v, want server draining", err)
	}
	rejectedCancelled := false
	if err := manager.RegisterTurn("thread-2", "turn-2", func() { rejectedCancelled = true }, 0, nil); !errors.Is(err, ErrServerDraining) {
		t.Fatalf("RegisterTurn() during drain error = %v, want server draining", err)
	}
	if !rejectedCancelled {
		t.Fatal("rejected turn was not cancelled")
	}
	if activeCancelled {
		t.Fatal("BeginDraining cancelled an already-admitted turn")
	}

	active := manager.BeginShutdown()
	if len(active) != 1 || active[0].ThreadID != "thread-1" || active[0].TurnID != "turn-1" {
		t.Fatalf("BeginShutdown() turns = %#v, want the admitted thread-1 turn", active)
	}
}
