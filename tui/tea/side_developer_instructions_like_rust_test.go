package tea

import (
	"strings"
	"testing"
)

// TestSideDeveloperInstructionsKeepExistingPolicyLikeRust covers the side-chat
// policy layer on the path the CLI actually uses (`app/side_tui.go` and
// `app/remote_tui.go` both call tea.SideDeveloperInstructions). tui/app keeps a
// second copy for its own callers; this test pins the production one so the
// verifier cannot pass while the live implementation drifts.
func TestSideDeveloperInstructionsKeepExistingPolicyLikeRust(t *testing.T) {
	got := SideDeveloperInstructions("Existing developer policy.")
	if !strings.Contains(got, "Existing developer policy.") {
		t.Fatalf("SideDeveloperInstructions() missing existing policy: %q", got)
	}
	if !strings.Contains(got, "You are in a side conversation, not the main thread.") {
		t.Fatalf("SideDeveloperInstructions() missing side policy: %q", got)
	}
	if !strings.Contains(got, "Sub-agents are off-limits in this side conversation.") {
		t.Fatalf("SideDeveloperInstructions() missing sub-agent restriction: %q", got)
	}
	if !strings.HasPrefix(got, "Existing developer policy.\n\n") {
		t.Fatalf("existing policy must lead the developer instructions: %q", got)
	}
	if got := SideDeveloperInstructions("   "); got != SideDeveloperInstructionText {
		t.Fatalf("blank SideDeveloperInstructions() = %q, want side text", got)
	}
}
