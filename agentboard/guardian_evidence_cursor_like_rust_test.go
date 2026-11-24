package agentboard

import (
	"encoding/json"
	"strings"
	"testing"
)

func guardianCursorTestSnapshot() *GuardianEvidenceSnapshot {
	source := json.RawMessage(`{"type":"response_item","role":"user","content":[{"type":"input_text","text":"evidence"}]}`)
	return &GuardianEvidenceSnapshot{
		Start: GuardianHistoryPosition{ThreadID: "root", EndOrdinalExclusive: 0, EndByteOffset: 0},
		End:   GuardianHistoryPosition{ThreadID: "root", EndOrdinalExclusive: 3, EndByteOffset: 30},
		Sources: []GuardianCapturedEvidence{
			{ID: GuardianEvidenceID{Ordinal: 0}, Original: source, End: GuardianHistoryPosition{ThreadID: "root", EndOrdinalExclusive: 1, EndByteOffset: 10}},
			{ID: GuardianEvidenceID{Ordinal: 1}, Original: source, End: GuardianHistoryPosition{ThreadID: "root", EndOrdinalExclusive: 2, EndByteOffset: 20}},
			{ID: GuardianEvidenceID{Ordinal: 2}, Original: source, End: GuardianHistoryPosition{ThreadID: "root", EndOrdinalExclusive: 3, EndByteOffset: 30}},
		},
	}
}

// Rust #62135 EvidenceCursor: pending returns the unacknowledged suffix and a
// partial acknowledgement lands on the consumed record's end, while a complete
// one lands on the capture's exclusive end (also skipping excluded trailers).
func TestGuardianEvidenceCursorAcknowledgesConsumedPrefixLikeRust(t *testing.T) {
	cursor := NewGuardianEvidenceCursor("root")
	snapshot := guardianCursorTestSnapshot()

	pending, err := cursor.Pending(snapshot)
	if err != nil {
		t.Fatalf("Pending() error = %v", err)
	}
	if len(pending) != 3 || pending[0].ID.Ordinal != 0 || pending[2].ID.Ordinal != 2 {
		t.Fatalf("pending = %#v, want every record in source order", pending)
	}

	if err := cursor.Acknowledge(snapshot, 2); err != nil {
		t.Fatalf("Acknowledge(2) error = %v", err)
	}
	if got := cursor.Position(); got.EndOrdinalExclusive != 2 || got.EndByteOffset != 20 {
		t.Fatalf("position after partial ack = %#v, want the second record's end", got)
	}
	pending, err = cursor.Pending(snapshot)
	if err != nil {
		t.Fatalf("Pending() error = %v", err)
	}
	if len(pending) != 1 || pending[0].ID.Ordinal != 2 {
		t.Fatalf("pending after partial ack = %#v", pending)
	}

	if err := cursor.Acknowledge(snapshot, 1); err != nil {
		t.Fatalf("Acknowledge(rest) error = %v", err)
	}
	if got, want := cursor.Position(), snapshot.End; got != want {
		t.Fatalf("position after complete ack = %#v, want %#v", got, want)
	}
	pending, err = cursor.Pending(snapshot)
	if err != nil {
		t.Fatalf("Pending() error = %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending after complete ack = %#v, want none", pending)
	}
}

// A capture that cannot contain the acknowledged position is refused instead of
// silently replaying evidence the board already consumed.
func TestGuardianEvidenceCursorRefusesForeignCapturesLikeRust(t *testing.T) {
	cursor := NewGuardianEvidenceCursor("root")
	snapshot := guardianCursorTestSnapshot()
	if err := cursor.Acknowledge(snapshot, len(snapshot.Sources)); err != nil {
		t.Fatalf("Acknowledge() error = %v", err)
	}

	otherRoot := guardianCursorTestSnapshot()
	otherRoot.Start.ThreadID = "other"
	otherRoot.End.ThreadID = "other"
	if _, err := cursor.Pending(otherRoot); err == nil || !strings.Contains(err.Error(), "does not contain the current cursor") {
		t.Fatalf("Pending(other root) error = %v", err)
	}

	stale := guardianCursorTestSnapshot()
	stale.End = GuardianHistoryPosition{ThreadID: "root", EndOrdinalExclusive: 1, EndByteOffset: 10}
	if _, err := cursor.Pending(stale); err == nil || !strings.Contains(err.Error(), "does not contain the current cursor") {
		t.Fatalf("Pending(stale capture) error = %v", err)
	}
	if err := cursor.Acknowledge(stale, 0); err == nil {
		t.Fatalf("Acknowledge(stale capture) succeeded")
	}
}

// An acknowledgement cannot run past the pending records.
func TestGuardianEvidenceCursorRejectsExcessAcknowledgementLikeRust(t *testing.T) {
	cursor := NewGuardianEvidenceCursor("root")
	snapshot := guardianCursorTestSnapshot()
	if err := cursor.Acknowledge(snapshot, len(snapshot.Sources)+1); err == nil || err.Error() != "acknowledgement exceeds pending evidence" {
		t.Fatalf("Acknowledge(excess) error = %v", err)
	}
	if got := cursor.Position(); got != (GuardianHistoryPosition{ThreadID: "root"}) {
		t.Fatalf("position after refused ack = %#v, want unchanged", got)
	}
}
