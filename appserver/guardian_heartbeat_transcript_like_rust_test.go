package appserver

import (
	"strings"
	"testing"

	"codex_go/session"
)

func guardianHeartbeatEnvelope(automationID, timestamp, instructions string) string {
	return "<heartbeat>\n  <automation_id>" + automationID +
		"</automation_id>\n  <current_time_iso>" + timestamp +
		"</current_time_iso>\n  <instructions>\n" + instructions +
		"\n  </instructions>\n</heartbeat>\n"
}

func guardianHeartbeatItem(id, automationID, timestamp, instructions string) session.Item {
	return session.Item{
		ID:   id,
		Type: "message",
		Role: "user",
		Text: guardianHeartbeatEnvelope(automationID, timestamp, instructions),
		Data: map[string]any{
			"internal_chat_message_metadata_passthrough": map[string]any{
				"content_item_kinds": []any{"user.heartbeat"},
			},
		},
		CreatedAt: fixedTime(),
	}
}

// Mirrors Rust #57519: scheduled heartbeat provenance is collected and the
// repeated instructions are compressed before the transcript is retained.
func TestGuardianTranscriptCompressesRepeatedHeartbeatsLikeRust(t *testing.T) {
	router := guardianProfileTestRouter(t, []session.Item{
		guardianHeartbeatItem("beat-1", "nightly", "2026-10-27T01:00:00Z", "check the build"),
		guardianHeartbeatItem("beat-2", "nightly", "2026-10-27T02:00:00Z", "check the build"),
	})
	selection := router.guardianReviewTranscriptSelection(guardianProfileTestThreadID)
	if len(selection.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(selection.Entries))
	}
	if !strings.Contains(selection.Entries[0].Text, "check the build") {
		t.Fatalf("first heartbeat = %q", selection.Entries[0].Text)
	}
	if !strings.Contains(selection.Entries[1].Text, "Instructions unchanged from transcript entry [1]") {
		t.Fatalf("repeated heartbeat = %q", selection.Entries[1].Text)
	}
}
