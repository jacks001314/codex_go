package appserver

import (
	"testing"

	"codex_go/session"
)

// Rust #62842 (`2b5111917c`) renders bundled, catalog and configured role
// guidance as tagged `multi_agent.role_instructions` messages and drops
// inherited untagged parent guidance from a fork by matching its body.
func TestForkStripsUntaggedParentRoleBodyLikeRust(t *testing.T) {
	const threadID = "thread-multi-agent-fork-role"
	router := newMultiAgentWorldStateTestRouter(t, threadID, "root_agent_usage_hint_text = \"Configured root guidance.\"\n")

	bodies := router.inheritedMultiAgentRoleBodies(threadID)
	found := false
	for _, body := range bodies {
		if body == "Configured root guidance." {
			found = true
		}
	}
	if !found {
		t.Fatalf("parent role bodies = %#v, want the configured root guidance", bodies)
	}

	items := []session.Item{
		{ID: "legacy-role", Type: "message", Role: "developer", Text: "Configured root guidance."},
		{ID: "tagged-role", Type: "message", Role: "developer", Text: multiAgentRoleOpenMarker + "Configured root guidance." + multiAgentRoleCloseMarker},
		{ID: "content-role", Type: "message", Role: "developer", Content: []session.ContentPart{{Type: "input_text", Text: "Configured root guidance."}}},
		{ID: "unrelated", Type: "message", Role: "developer", Text: "Unrelated developer note."},
	}
	filtered := filterInheritedCurrentTimeReminders(items, bodies)
	if len(filtered) != 1 || filtered[0].ID != "unrelated" {
		t.Fatalf("filtered fork history = %#v, want only the unrelated developer note", filtered)
	}
}
