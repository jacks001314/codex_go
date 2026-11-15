package modelfacingid

import (
	"testing"
)

type testModelIDKey map[string]any

func (testModelIDKey) ModelIDPrefix() string { return "env" }

func TestModelIDKeyCanonicalizesObjectsLikeRust(t *testing.T) {
	left, err := NewModelIDKey(testModelIDKey{"b": 2, "a": map[string]any{"d": 4, "c": 3}})
	if err != nil {
		t.Fatalf("NewModelIDKey(left) error = %v", err)
	}
	right, err := NewModelIDKey(testModelIDKey{"a": map[string]any{"c": 3, "d": 4}, "b": 2})
	if err != nil {
		t.Fatalf("NewModelIDKey(right) error = %v", err)
	}
	if left != right {
		t.Fatalf("canonical keys differ: %#v and %#v", left, right)
	}
	if left.Kind != "env" || left.Key != `{"a":{"c":3,"d":4},"b":2}` {
		t.Fatalf("key = %#v", left)
	}
}

func TestModelIDStateIDAtMatchesIssuedSequenceLikeRust(t *testing.T) {
	generator, err := New(42, "env")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	state := generator.Snapshot()
	issued := make([]ID, 0, 5)
	for range 5 {
		issued = append(issued, generator.Next())
	}
	for ordinal, want := range issued {
		got, err := state.IDAt(uint64(ordinal))
		if err != nil {
			t.Fatalf("IDAt(%d) error = %v", ordinal, err)
		}
		if got != want {
			t.Fatalf("IDAt(%d) = %v, want %v", ordinal, got, want)
		}
	}
}

func TestEnvironmentIDAliasesLikeRust(t *testing.T) {
	key, err := NewModelIDKey(RegisteredEnvironmentID("registered"))
	if err != nil {
		t.Fatalf("NewModelIDKey() error = %v", err)
	}
	if key.Kind != "env" || key.Key != `"registered"` {
		t.Fatalf("key = %#v", key)
	}
	idMap, err := NewModelIDMap(7, "env")
	if err != nil {
		t.Fatalf("NewModelIDMap() error = %v", err)
	}
	id, err := idMap.Initial.IDAt(0)
	if err != nil {
		t.Fatalf("IDAt() error = %v", err)
	}
	if got := ModelEnvironmentIDFromID(id).String(); got != "env"+id.Digits() {
		t.Fatalf("environment alias = %q, want env prefix with %q digits", got, id.Digits())
	}
	if got := (ModelEnvironmentID(1000)).String(); got != "env001000" {
		t.Fatalf("ModelEnvironmentID(1000) = %q", got)
	}
}
