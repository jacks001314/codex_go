package state

import (
	"strings"
	"testing"
)

// Mirrors Rust #57494: the approval reason is bounded to 512 estimated tokens
// during synchronous request preparation and the bounded text reaches both the
// action JSON and the rendered framing.
func TestGuardianApprovalReasonIsBudgetedLikeRust(t *testing.T) {
	reason := strings.Repeat("r", 512*4+2048)
	rendered, err := joinPlannedAction(Action{
		Type:     "mcp_tool_call",
		Server:   "calendar",
		ToolName: "create_event",
		Reason:   reason,
	}, ActionPresentationSyncFull)
	if err != nil {
		t.Fatalf("joinPlannedAction() error = %v", err)
	}
	if strings.Contains(rendered, reason) {
		t.Fatal("approval reason was rendered without the preparation budget")
	}
	if !strings.Contains(rendered, `<truncated omitted_approx_tokens="`) {
		t.Fatalf("approval reason is missing the truncation marker: %q", rendered)
	}
	if got, limit := len(rendered), 512*4+4096; got > limit {
		t.Fatalf("rendered approval request bytes = %d, want <= %d", got, limit)
	}
}
