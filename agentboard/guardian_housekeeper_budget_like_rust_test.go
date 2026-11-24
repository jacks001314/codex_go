package agentboard

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestGuardianRoutingBatchPrefixLikeRust pins Rust #62372's batch sizing: the
// catalog and array delimiters count first, each original adds its serialized
// bytes plus the separating comma, and the prefix stops before the first
// original that does not fit.
func TestGuardianRoutingBatchPrefixLikeRust(t *testing.T) {
	catalog := map[string]*string{"release": guardianHousekeeperText("Shipped work")}
	sources := []GuardianRoutingSource{
		{ID: GuardianEvidenceID{Ordinal: 1}, Text: guardianEvidenceText(1, "one")},
		{ID: GuardianEvidenceID{Ordinal: 2}, Text: guardianEvidenceText(2, "two")},
		{ID: GuardianEvidenceID{Ordinal: 3}, Text: guardianEvidenceText(3, "three")},
	}
	empty, err := json.Marshal(guardianRoutingInput{Catalog: catalog, Sources: []GuardianRoutingSource{}})
	if err != nil {
		t.Fatalf("marshal empty input: %v", err)
	}
	first, err := json.Marshal(sources[0])
	if err != nil {
		t.Fatalf("marshal first source: %v", err)
	}
	second, err := json.Marshal(sources[1])
	if err != nil {
		t.Fatalf("marshal second source: %v", err)
	}

	// Room for exactly two originals.
	budget := len(empty) + len(first) + len(second) + 1
	if got, err := GuardianRoutingBatchPrefix(catalog, sources, budget); err != nil || got != 2 {
		t.Fatalf("prefix = %d, %v; want 2", got, err)
	}
	// One byte less drops the second original.
	if got, err := GuardianRoutingBatchPrefix(catalog, sources, budget-1); err != nil || got != 1 {
		t.Fatalf("tight prefix = %d, %v; want 1", got, err)
	}
	// An empty batch needs nothing.
	if got, err := GuardianRoutingBatchPrefix(catalog, nil, 0); err != nil || got != 0 {
		t.Fatalf("empty sources = %d, %v", got, err)
	}
	// A catalog that alone exceeds the budget stops the pass, like Rust's
	// ensure! when no original fits.
	if got, err := GuardianRoutingBatchPrefix(catalog, sources, len(empty)); err == nil ||
		!strings.Contains(err.Error(), "exceed the model's tool-output budget") || got != 0 {
		t.Fatalf("over-budget batch = %d, %v", got, err)
	}
	// A single original above the per-post bound fails instead of being split.
	oversized := []GuardianRoutingSource{{ID: GuardianEvidenceID{Ordinal: 1}, Text: strings.Repeat("x", GuardianRoutingMaxSourceJSONBytes)}}
	if _, err := GuardianRoutingBatchPrefix(catalog, oversized, GuardianRoutingMaxSourceJSONBytes*4); err == nil ||
		!strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized original error = %v", err)
	}
}

// TestGuardianRoutingBatchPrefixStopsAtFirstOverflowLikeRust keeps whole
// originals: a large record after a fitting one ends the prefix instead of
// admitting a later, smaller record.
func TestGuardianRoutingBatchPrefixStopsAtFirstOverflowLikeRust(t *testing.T) {
	catalog := map[string]*string{}
	sources := []GuardianRoutingSource{
		{ID: GuardianEvidenceID{Ordinal: 1}, Text: guardianEvidenceText(1, "small")},
		{ID: GuardianEvidenceID{Ordinal: 2}, Text: guardianEvidenceText(2, strings.Repeat("y", 400))},
		{ID: GuardianEvidenceID{Ordinal: 3}, Text: guardianEvidenceText(3, "tiny")},
	}
	empty, err := json.Marshal(guardianRoutingInput{Catalog: catalog, Sources: []GuardianRoutingSource{}})
	if err != nil {
		t.Fatalf("marshal empty input: %v", err)
	}
	first, err := json.Marshal(sources[0])
	if err != nil {
		t.Fatalf("marshal first source: %v", err)
	}
	third, err := json.Marshal(sources[2])
	if err != nil {
		t.Fatalf("marshal third source: %v", err)
	}
	// The budget still has room for the tiny third record, yet the oversized
	// second one ends the prefix: whole originals are never reordered.
	budget := len(empty) + len(first) + len(third) + 1
	if got, err := GuardianRoutingBatchPrefix(catalog, sources, budget); err != nil || got != 1 {
		t.Fatalf("prefix = %d, %v; want 1", got, err)
	}
}
