package tea

import (
	"strings"
	"testing"
)

func TestSidePromptsForbidManagedWorktreeCreationLikeRust(t *testing.T) {
	// Rust keeps the worktree restriction on the temporary policy only; the
	// shared boundary and attached policy describe the same boundary.
	const instruction = "This temporary side chat cannot retain a worktree. Direct requests for a new worktree to the main conversation without calling create_worktree."
	if strings.Contains(SideBoundaryPrompt, instruction) {
		t.Fatalf("shared boundary prompt carries the temporary worktree restriction")
	}
	if !strings.Contains(SideDeveloperInstructionsFor("", SideChatStorageTemporary), instruction) {
		t.Fatalf("temporary developer instructions are missing the managed-worktree restriction")
	}
	if strings.Contains(SideDeveloperInstructionsFor("", SideChatStorageAttached), instruction) {
		t.Fatalf("attached developer instructions kept the temporary worktree restriction")
	}
}
