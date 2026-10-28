package agent

import (
	"errors"
	"testing"
)

// Mirrors Rust #57586 invocation_tests: a reservation holds the call ID through
// dispatch, publishing happens only after activation, duplicate active IDs in a
// turn are rejected, and ending the call revokes lookups.
func TestInvocationRegistryTracksLiveCallLifetimeLikeRust(t *testing.T) {
	registry := NewInvocationRegistry()
	reservation, err := registry.Reserve("turn-1", "shared")
	if err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}
	if reservation == nil {
		t.Fatal("Reserve() returned no reservation")
	}
	// The key is held before pre-tool hooks admit the call, but it is not
	// published as a live invocation yet.
	if _, err := registry.LookupToolInvocation("thread-1", "turn-1", "shared"); !errors.Is(err, ErrToolCallNotActive) {
		t.Fatalf("LookupToolInvocation() before activation error = %v, want ErrToolCallNotActive", err)
	}
	// A duplicate active ID in the same turn is rejected with the model-facing
	// message Rust produces.
	if _, err := registry.Reserve("turn-1", "shared"); err == nil || err.Error() != "tool call ID shared is already active in this turn" {
		t.Fatalf("duplicate Reserve() error = %v", err)
	}
	// The same ID in another turn does not collide.
	otherTurn, err := registry.Reserve("turn-2", "shared")
	if err != nil {
		t.Fatalf("Reserve() in another turn error = %v", err)
	}

	guard := reservation.Activate()
	if guard == nil {
		t.Fatal("Activate() returned no guard")
	}
	live, err := registry.LookupToolInvocation("thread-1", "turn-1", "shared")
	if err != nil {
		t.Fatalf("LookupToolInvocation() error = %v", err)
	}
	if live.ThreadID != "thread-1" || live.TurnID != "turn-1" || live.CallID != "shared" || !live.Active() {
		t.Fatalf("live invocation = %#v", live)
	}
	// Activating twice does not republish an ended call.
	if again := reservation.Activate(); again != nil {
		t.Fatal("Activate() republished the call")
	}

	guard.Release()
	if live.Active() {
		t.Fatal("released invocation still reports Active()")
	}
	if _, err := registry.LookupToolInvocation("thread-1", "turn-1", "shared"); !errors.Is(err, ErrToolCallNotActive) {
		t.Fatalf("LookupToolInvocation() after release error = %v, want ErrToolCallNotActive", err)
	}

	// Releasing the reservation lets a later turn reuse the ID.
	otherTurn.Release()
	reservation.Release()
	reused, err := registry.Reserve("turn-1", "shared")
	if err != nil {
		t.Fatalf("Reserve() after release error = %v", err)
	}
	reused.Release()
}

// A nil registry is the opt-out: pure dispatch keeps working and lookups report
// the call as inactive.
func TestNilInvocationRegistryLikeRust(t *testing.T) {
	var registry *InvocationRegistry
	reservation, err := registry.Reserve("turn-1", "call-1")
	if err != nil || reservation != nil {
		t.Fatalf("nil Reserve() = %v, %v", reservation, err)
	}
	if _, err := registry.LookupToolInvocation("thread-1", "turn-1", "call-1"); !errors.Is(err, ErrToolCallNotActive) {
		t.Fatalf("nil LookupToolInvocation() error = %v", err)
	}
	var guard *InvocationGuard
	guard.Release()
	reservation.Release()
}
