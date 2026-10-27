package state

import (
	"strings"
	"testing"
)

func heartbeatEvidence(number int, automationID, timestamp, instructions string) TranscriptEvidenceEntry {
	return TranscriptEvidenceEntry{
		Kind:      GuardianTranscriptUser,
		Role:      "user",
		Text:      instructions,
		Scheduled: true,
		Heartbeat: &TranscriptHeartbeat{
			AutomationID: automationID,
			Timestamp:    timestamp,
			Instructions: instructions,
		},
	}
}

// Mirrors Rust #57519: repeated scheduler instructions become a reference to
// the earlier transcript entry, while changed instructions stay complete.
func TestCompressGuardianTranscriptHeartbeatsLikeRust(t *testing.T) {
	entries := []TranscriptEvidenceEntry{
		heartbeatEvidence(1, "nightly", "2026-10-27T01:00:00Z", "check the build"),
		{Kind: GuardianTranscriptUser, Role: "user", Text: "ordinary"},
		heartbeatEvidence(2, "nightly", "2026-10-27T02:00:00Z", "check the build"),
		heartbeatEvidence(3, "nightly", "2026-10-27T03:00:00Z", "publish the report"),
		heartbeatEvidence(4, "nightly", "2026-10-27T04:00:00Z", "check the build"),
	}
	CompressGuardianTranscriptHeartbeats(entries, 0)
	if entries[0].Text != "check the build" {
		t.Fatalf("first instructions = %q", entries[0].Text)
	}
	if want := "Scheduled automation nightly ran at 2026-10-27T02:00:00Z. Instructions unchanged from transcript entry [1]; this is a replay of that earlier instruction, not a new human instruction. If the referenced instructions are unavailable, do not infer authorization from this reference."; entries[2].Text != want {
		t.Fatalf("repeated instructions = %q, want %q", entries[2].Text, want)
	}
	if entries[3].Text != "publish the report" {
		t.Fatalf("changed instructions = %q", entries[3].Text)
	}
	if entries[4].Text != "check the build" {
		t.Fatalf("instructions after a change = %q", entries[4].Text)
	}
	for _, entry := range entries {
		if entry.Heartbeat != nil {
			t.Fatalf("compression retained heartbeat provenance: %#v", entry)
		}
	}
}

func TestCompressGuardianTranscriptHeartbeatsKeepsAbsoluteNumbers(t *testing.T) {
	entries := []TranscriptEvidenceEntry{
		heartbeatEvidence(11, "nightly", "2026-10-27T01:00:00Z", "check the build"),
		heartbeatEvidence(12, "nightly", "2026-10-27T02:00:00Z", "check the build"),
	}
	CompressGuardianTranscriptHeartbeats(entries, 10)
	if !strings.Contains(entries[1].Text, "transcript entry [11]") {
		t.Fatalf("delta reference = %q, want absolute entry [11]", entries[1].Text)
	}
}

func TestCompressGuardianTranscriptHeartbeatsResetsOnUnparsedScheduled(t *testing.T) {
	entries := []TranscriptEvidenceEntry{
		heartbeatEvidence(1, "nightly", "2026-10-27T01:00:00Z", "check the build"),
		{Kind: GuardianTranscriptUser, Role: "user", Text: "<heartbeat>unparsed", Scheduled: true},
		heartbeatEvidence(3, "nightly", "2026-10-27T03:00:00Z", "check the build"),
	}
	CompressGuardianTranscriptHeartbeats(entries, 0)
	if entries[2].Text != "check the build" {
		t.Fatalf("unparsed scheduled entry must clear references, got %q", entries[2].Text)
	}
}
