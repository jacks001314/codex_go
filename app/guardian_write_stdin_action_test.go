package app

import (
	"testing"

	"codex_go/appserver"
	chatwidget "codex_go/tui/chatwidget"
)

// TestRemoteGuardianWriteStdinActionLikeRust covers Rust #40528's app-server
// action projection into the TUI review action.
func TestRemoteGuardianWriteStdinActionLikeRust(t *testing.T) {
	action := remoteGuardianAssessmentAction(appserver.GuardianApprovalReviewAction{
		Type:       "writeStdin",
		ApprovalID: "stdin-approval",
		ProcessID:  "42",
		Stdin:      "confirm\n",
		CWD:        "/tmp/project",
	})
	if action.Kind != chatwidget.GuardianActionWriteStdin {
		t.Fatalf("kind = %q", action.Kind)
	}
	if action.ProcessID != "42" || action.Stdin != "confirm\n" || action.CWD != "/tmp/project" {
		t.Fatalf("action = %#v", action)
	}
	if got, want := chatwidget.GuardianActionSummary(action), `send input to terminal 42: "confirm\n"`; got != want {
		t.Fatalf("summary = %q, want %q", got, want)
	}
}
