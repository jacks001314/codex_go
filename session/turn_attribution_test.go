package session

import (
	"testing"
	"time"
)

// Rust #51402 (`551bd409eb`) `core/src/agent/control/spawn.rs` clears the
// inherited regular-turn attribution when a forked agent inherits compacted
// state: the fork starts its own turns, so the source thread's provenance does
// not apply to it.
func TestForkClearsInheritedTurnAttributionLikeRust(t *testing.T) {
	trigger := "user"
	source := &Record{
		ID: "thread-1",
		Metadata: Metadata{
			TurnAttribution: &TurnAttribution{TurnID: "turn-1", TurnTrigger: &trigger},
		},
	}
	forked := forkMetadata(source, ForkOptions{}, time.Now().UTC(), 0)
	if forked.TurnAttribution != nil {
		t.Fatalf("forked attribution = %#v, want cleared", forked.TurnAttribution)
	}
	if source.Metadata.TurnAttribution == nil {
		t.Fatalf("fork must not mutate the source attribution")
	}
}

// cloneMetadata deep-copies the attribution so a cloned record cannot share the
// pointer with its source.
func TestCloneMetadataDeepCopiesTurnAttributionLikeRust(t *testing.T) {
	trigger := "user"
	source := Metadata{TurnAttribution: &TurnAttribution{TurnID: "turn-1", TurnTrigger: &trigger}}
	clone := cloneMetadata(source)
	if clone.TurnAttribution == nil || clone.TurnAttribution == source.TurnAttribution {
		t.Fatalf("clone = %#v, want a distinct copy", clone.TurnAttribution)
	}
	*clone.TurnAttribution.TurnTrigger = "automation"
	if *source.TurnAttribution.TurnTrigger != "user" {
		t.Fatalf("mutating the clone changed the source: %q", *source.TurnAttribution.TurnTrigger)
	}
}
