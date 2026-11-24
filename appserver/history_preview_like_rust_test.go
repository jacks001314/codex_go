package appserver

import (
	"testing"

	"codex_go/session"
)

// Mirrors Rust #63603 for the state-backed thread list: the AGENTS.md
// instruction block is skipped and prior context is stripped from the preview.
func TestHistoryPreviewSkipsAgentsInstructionsAndStripsContextLikeRust(t *testing.T) {
	items := []session.Item{
		{Role: "user", Text: "# AGENTS.md instructions for project\n\n<INSTRUCTIONS>\nbody\n</INSTRUCTIONS>"},
		{Role: "user", Text: "<prior context> <user_message>Count to 5"},
	}
	if got := historyPreview(items); got != "Count to 5" {
		t.Fatalf("historyPreview = %q, want %q", got, "Count to 5")
	}
}
