package appserver

import (
	"strings"
	"testing"

	"codex_go/sandbox"
	"codex_go/tool"
)

// Mirrors Rust #41159: the complete formatted stdin action plus its reason must
// fit before approval; oversized or formatter-truncated actions are rejected.
func TestGuardStdinReviewSizeLikeRust(t *testing.T) {
	reason := terminalWriteApprovalReason(&tool.WriteStdinApprovalRequest{}, sandbox.SandboxPermissionsRequireEscalated)
	within := terminalWriteApprovalAction(&tool.WriteStdinApprovalRequest{
		ProcessID:          1,
		CWD:                "/repo",
		Chars:              "yes\n",
		SandboxPermissions: sandbox.SandboxPermissionsRequireEscalated,
	}, sandbox.SandboxPermissionsRequireEscalated)
	if err := guardStdinReviewSize(within, reason); err != nil {
		t.Fatalf("small terminal input rejected: %v", err)
	}

	// Raw bytes fit in 8KB, but JSON escaping pushes the formatted action over.
	oversized := terminalWriteApprovalAction(&tool.WriteStdinApprovalRequest{
		ProcessID:          1,
		CWD:                "/repo",
		Chars:              strings.Repeat(`"`, 4_100) + "\nREJECTED=1\n",
		SandboxPermissions: sandbox.SandboxPermissionsRequireEscalated,
	}, sandbox.SandboxPermissionsRequireEscalated)
	if err := guardStdinReviewSize(oversized, reason); err == nil ||
		!strings.Contains(err.Error(), "too large to review safely") {
		t.Fatalf("oversized terminal input error = %v", err)
	}

	truncated := terminalWriteApprovalAction(&tool.WriteStdinApprovalRequest{
		ProcessID:          1,
		CWD:                "/repo",
		Chars:              strings.Repeat("a", 250_000),
		SandboxPermissions: sandbox.SandboxPermissionsRequireEscalated,
	}, sandbox.SandboxPermissionsRequireEscalated)
	if err := guardStdinReviewSize(truncated, reason); err == nil ||
		!strings.Contains(err.Error(), "too large to review safely") {
		t.Fatalf("truncated terminal input error = %v", err)
	}
}
