package rollout

import (
	"testing"

	"codex_go/session"
)

// Mirrors Rust #63603
// app-server/src/request_processors/thread_processor_tests.rs
// ::preview_from_rollout_items_prefers_plain_user_messages: the AGENTS.md
// instruction block is hidden context, and the preview keeps only the text
// after the user message marker.
func TestPreviewFromSessionItemsPrefersPlainUserMessagesLikeRust(t *testing.T) {
	items := []session.Item{
		{Role: "user", Text: "# AGENTS.md instructions for project\n\n<INSTRUCTIONS>\n<AGENTS.md contents>\n</INSTRUCTIONS>"},
		{Role: "user", Text: "<prior context> <user_message>Count to 5"},
	}
	if got := previewFromSessionItems(items); got != "Count to 5" {
		t.Fatalf("preview = %q, want %q", got, "Count to 5")
	}
}

// A thread whose only user content is contextual falls through to the rest of
// the rollout instead of leaking the instructions block.
func TestPreviewFromSessionItemsSkipsContextOnlyRollout(t *testing.T) {
	items := []session.Item{
		{Role: "user", Text: "# AGENTS.md instructions for project\n\n<INSTRUCTIONS>\nbody\n</INSTRUCTIONS>"},
		{Role: "assistant", Text: "Ready"},
	}
	if got := previewFromSessionItems(items); got != "Ready" {
		t.Fatalf("preview = %q, want %q", got, "Ready")
	}
}
