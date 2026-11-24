package agentboard

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestGuardianRoutedOriginalEncodingLikeRust mirrors Rust #62135: the stored
// evidence body is {"id":{"ordinal":N},"original":<item>} and decodes back to
// the same ordinal and item.
func TestGuardianRoutedOriginalEncodingLikeRust(t *testing.T) {
	original := json.RawMessage(`{"type":"response_item","payload":{"type":"function_call_output","output":"hello"}}`)
	text, err := EncodeGuardianRoutedOriginal(GuardianEvidenceID{Ordinal: 4}, original)
	if err != nil {
		t.Fatalf("EncodeGuardianRoutedOriginal() error = %v", err)
	}
	if !strings.HasPrefix(text, `{"id":{"ordinal":4},"original":`) {
		t.Fatalf("encoded original = %q", text)
	}
	decoded, err := parseGuardianRoutedOriginal(text)
	if err != nil {
		t.Fatalf("parseGuardianRoutedOriginal() error = %v", err)
	}
	if decoded.ID.Ordinal != 4 || string(decoded.Original) != string(original) {
		t.Fatalf("decoded = %#v", decoded)
	}
}

// TestGuardianRoutedOriginalRejectsMalformedItemsLikeRust keeps prose,
// trailing values and empty objects out of the stored evidence body.
func TestGuardianRoutedOriginalRejectsMalformedItemsLikeRust(t *testing.T) {
	cases := []string{
		"",
		"not json",
		`{"a":1} {"b":2}`,
		`{}`,
		`[1,2]`,
	}
	for _, item := range cases {
		if _, err := EncodeGuardianRoutedOriginal(GuardianEvidenceID{Ordinal: 1}, json.RawMessage(item)); err == nil {
			t.Fatalf("EncodeGuardianRoutedOriginal(%q) accepted a malformed item", item)
		}
	}
}

// TestGuardianEvidenceSnapshotEncodesSourcesLikeRust renders every captured
// original in source order and compares start with end for source progress,
// the same way Rust EvidenceSnapshot::source_advanced does.
func TestGuardianEvidenceSnapshotEncodesSourcesLikeRust(t *testing.T) {
	snapshot := GuardianEvidenceSnapshot{
		Start: GuardianHistoryPosition{ThreadID: "root", EndOrdinalExclusive: 1, EndByteOffset: 10},
		End:   GuardianHistoryPosition{ThreadID: "root", EndOrdinalExclusive: 3, EndByteOffset: 30},
		Sources: []GuardianCapturedEvidence{
			{ID: GuardianEvidenceID{Ordinal: 1}, Original: json.RawMessage(`{"type":"response_item"}`), End: GuardianHistoryPosition{ThreadID: "root", EndOrdinalExclusive: 2, EndByteOffset: 20}},
			{ID: GuardianEvidenceID{Ordinal: 2}, Original: json.RawMessage(`{"type":"response_item"}`), End: GuardianHistoryPosition{ThreadID: "root", EndOrdinalExclusive: 3, EndByteOffset: 30}},
		},
	}
	encoded, err := snapshot.EncodedSources()
	if err != nil {
		t.Fatalf("EncodedSources() error = %v", err)
	}
	if len(encoded) != 2 || !strings.Contains(encoded[0], `"ordinal":1`) || !strings.Contains(encoded[1], `"ordinal":2`) {
		t.Fatalf("encoded sources = %#v", encoded)
	}
	if snapshot.Cutoff() != snapshot.End {
		t.Fatalf("Cutoff() = %#v, want %#v", snapshot.Cutoff(), snapshot.End)
	}
	if !snapshot.SourceAdvanced() {
		t.Fatal("SourceAdvanced() = false although start and end differ")
	}
	settled := snapshot
	settled.Start = settled.End
	if settled.SourceAdvanced() {
		t.Fatal("SourceAdvanced() = true although start equals end")
	}
	if (GuardianEvidenceSnapshot{}).SourceAdvanced() {
		t.Fatal("zero snapshot reported advanced sources")
	}
}
