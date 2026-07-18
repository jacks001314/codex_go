package app

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"codex_go/appserver"
)

func TestSideBoundaryPromptMarksInheritedHistoryReferenceOnlyMatchRust(t *testing.T) {
	for _, want := range []string{
		"Side conversation boundary.",
		"Everything before this boundary is inherited history",
		"It is not your current task.",
		"Only messages submitted after this boundary are active",
		"Do not continue, execute, or complete",
		"separate from the main thread",
		"External tools may be available according to this thread's current",
		"Any tool calls or outputs visible before this boundary happened",
		"Sub-agents are off-limits in this side conversation.",
		"Do not modify files",
	} {
		if !strings.Contains(SideBoundaryPrompt, want) {
			t.Fatalf("SideBoundaryPrompt missing %q", want)
		}
	}
}

func TestSideStartErrorMessageMatchRust(t *testing.T) {
	err := fmt.Errorf("thread/fork failed during TUI bootstrap: %w", errors.New("thread/fork failed: no rollout found for thread id 019da1a1"))
	if got := SideStartErrorMessage(err); got != SideNoStartedConversationMessage {
		t.Fatalf("SideStartErrorMessage(no rollout) = %q, want %q", got, SideNoStartedConversationMessage)
	}

	err = errors.New("includeTurns is unavailable before first user message")
	if got := SideStartErrorMessage(err); got != SideNoStartedConversationMessage {
		t.Fatalf("SideStartErrorMessage(includeTurns) = %q, want %q", got, SideNoStartedConversationMessage)
	}

	err = errors.New("transport disconnected")
	if got := SideStartErrorMessage(err); got != "Failed to start side conversation: transport disconnected" {
		t.Fatalf("SideStartErrorMessage(generic) = %q", got)
	}
	err = errors.New(" transport disconnected ")
	if got := SideStartErrorMessage(err); got != "Failed to start side conversation:  transport disconnected " {
		t.Fatalf("SideStartErrorMessage(preserve spaces) = %q", got)
	}
}

func TestSideDeveloperInstructionsAppendsExistingPolicyMatchRust(t *testing.T) {
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
	if got := SideDeveloperInstructions("   "); got != SideDeveloperInstructionText {
		t.Fatalf("blank SideDeveloperInstructions() = %q, want side text", got)
	}
}

func TestSideParentStatusLabelsAndActionableMatchRust(t *testing.T) {
	cases := []struct {
		status     SideParentStatus
		mainLabel  string
		parentText string
		actionable bool
	}{
		{SideParentStatusNeedsInput, "main needs input", "parent needs input", true},
		{SideParentStatusNeedsApproval, "main needs approval", "parent needs approval", true},
		{SideParentStatusFailed, "main failed", "parent failed", false},
		{SideParentStatusInterrupted, "main interrupted", "parent interrupted", false},
		{SideParentStatusClosed, "main closed", "parent closed", false},
		{SideParentStatusFinished, "main finished", "parent finished", false},
	}
	for _, tc := range cases {
		if got := tc.status.Label(true); got != tc.mainLabel {
			t.Fatalf("%s main label = %q, want %q", tc.status, got, tc.mainLabel)
		}
		if got := tc.status.Label(false); got != tc.parentText {
			t.Fatalf("%s parent label = %q, want %q", tc.status, got, tc.parentText)
		}
		if got := tc.status.IsActionable(); got != tc.actionable {
			t.Fatalf("%s actionable = %v, want %v", tc.status, got, tc.actionable)
		}
	}
}

func TestSideParentStatusForRequestKindMatchRust(t *testing.T) {
	if got, ok := SideParentStatusForRequestKind(ServerRequestUserInput); !ok || got != SideParentStatusNeedsInput {
		t.Fatalf("user input request status = %q/%v", got, ok)
	}
	for _, kind := range []string{
		ServerRequestCommandExecutionApproval,
		ServerRequestFileChangeApproval,
		ServerRequestMcpElicitation,
		ServerRequestPermissionsApproval,
		ServerRequestApplyPatchApproval,
		ServerRequestExecCommandApproval,
		"applyPatchApproval",
		"execCommandApproval",
	} {
		if got, ok := SideParentStatusForRequestKind(kind); !ok || got != SideParentStatusNeedsApproval {
			t.Fatalf("%s request status = %q/%v", kind, got, ok)
		}
	}
	if got, ok := SideParentStatusForRequestKind("dynamic_tool_call"); ok || got != "" {
		t.Fatalf("dynamic request status = %q/%v, want none", got, ok)
	}
	if got, ok := SideParentStatusForRequestKind(" " + ServerRequestUserInput + " "); ok || got != "" {
		t.Fatalf("spaced request status = %q/%v, want none", got, ok)
	}
}

func TestSideParentStatusChangeForNotificationMatchRust(t *testing.T) {
	cases := []struct {
		kind       string
		turnStatus appserver.TurnStatus
		want       SideParentStatusChange
	}{
		{ServerNotificationTurnStarted, "", SideParentStatusChange{Kind: SideParentStatusChangeClear}},
		{ServerNotificationTurnCompleted, appserver.TurnStatusCompleted, SideParentStatusChange{Kind: SideParentStatusChangeSet, Status: SideParentStatusFinished}},
		{ServerNotificationTurnCompleted, appserver.TurnStatusInterrupted, SideParentStatusChange{Kind: SideParentStatusChangeSet, Status: SideParentStatusInterrupted}},
		{ServerNotificationTurnCompleted, appserver.TurnStatusFailed, SideParentStatusChange{Kind: SideParentStatusChangeSet, Status: SideParentStatusFailed}},
		{ServerNotificationThreadClosed, "", SideParentStatusChange{Kind: SideParentStatusChangeSet, Status: SideParentStatusClosed}},
		{ServerNotificationItemStarted, "", SideParentStatusChange{Kind: SideParentStatusChangeClearActionable}},
		{ServerNotificationServerRequestResolved, "", SideParentStatusChange{Kind: SideParentStatusChangeClearActionable}},
	}
	for _, tc := range cases {
		got, ok := SideParentStatusChangeForNotification(tc.kind, tc.turnStatus)
		if !ok || got != tc.want {
			t.Fatalf("SideParentStatusChangeForNotification(%s,%s) = %#v/%v, want %#v/true", tc.kind, tc.turnStatus, got, ok, tc.want)
		}
	}
	if got, ok := SideParentStatusChangeForNotification(ServerNotificationTurnCompleted, appserver.TurnStatusInProgress); ok || got != (SideParentStatusChange{}) {
		t.Fatalf("in-progress turn change = %#v/%v, want none", got, ok)
	}
	if got, ok := SideParentStatusChangeForNotification(" "+ServerNotificationTurnStarted+" ", ""); ok || got != (SideParentStatusChange{}) {
		t.Fatalf("spaced notification change = %#v/%v, want none", got, ok)
	}
}

func TestApplySideParentStatusChangeAndContextLabelMatchRust(t *testing.T) {
	state := NewSideThreadState("parent", "side")
	if !ApplySideParentStatusChange(&state, SideParentStatusChange{Kind: SideParentStatusChangeSet, Status: SideParentStatusNeedsApproval}) {
		t.Fatal("set needs approval should change state")
	}
	if state.ParentStatus != SideParentStatusNeedsApproval {
		t.Fatalf("parent status = %q", state.ParentStatus)
	}
	if got := SideContextLabel(true, "", state.ParentStatus); got != "Side from main thread · main needs approval · Ctrl+C to return" {
		t.Fatalf("main context label = %q", got)
	}
	if got := SideContextLabel(false, "worker", SideParentStatusFinished); got != "Side from parent thread (worker) · parent finished · Ctrl+C to return" {
		t.Fatalf("parent context label = %q", got)
	}
	if got := SideContextLabel(false, " worker ", ""); got != "Side from parent thread ( worker ) · Ctrl+C to return" {
		t.Fatalf("parent raw label = %q", got)
	}
	if !ApplySideParentStatusChange(&state, SideParentStatusChange{Kind: SideParentStatusChangeClearActionable}) {
		t.Fatal("clear actionable should change needs approval")
	}
	if state.ParentStatus != "" {
		t.Fatalf("parent status after clear actionable = %q", state.ParentStatus)
	}
	state.ParentStatus = SideParentStatusFailed
	if ApplySideParentStatusChange(&state, SideParentStatusChange{Kind: SideParentStatusChangeClearActionable}) {
		t.Fatal("clear actionable should not change failed")
	}
	if !ApplySideParentStatusChange(&state, SideParentStatusChange{Kind: SideParentStatusChangeClear}) {
		t.Fatal("clear should change failed")
	}
}

func TestSideStartBlockAndCloseErrorMessages(t *testing.T) {
	if got, ok := SideStartBlockMessage(false, 0); !ok || got != SideMainThreadUnavailableMessage {
		t.Fatalf("SideStartBlockMessage(no primary) = %q/%v", got, ok)
	}
	if got, ok := SideStartBlockMessage(true, 1); !ok || got != SideAlreadyOpenMessage {
		t.Fatalf("SideStartBlockMessage(open) = %q/%v", got, ok)
	}
	if got, ok := SideStartBlockMessage(true, 0); ok || got != "" {
		t.Fatalf("SideStartBlockMessage(clear) = %q/%v", got, ok)
	}
	if got := SideCloseErrorMessage(rustThreadID1, errors.New("transport disconnected")); got != "Failed to close side conversation "+rustThreadID1+"; it is still open: transport disconnected" {
		t.Fatalf("SideCloseErrorMessage() = %q", got)
	}
	if got := SideCloseErrorMessage("00000000-0000-0000-0000-0000000000AA", errors.New("transport disconnected")); got != "Failed to close side conversation 00000000-0000-0000-0000-0000000000aa; it is still open: transport disconnected" {
		t.Fatalf("SideCloseErrorMessage(uppercase) = %q", got)
	}
	if got := SideCloseErrorMessage(" "+rustThreadID1+" ", errors.New("transport disconnected")); got != "Failed to close side conversation side conversation; it is still open: transport disconnected" {
		t.Fatalf("SideCloseErrorMessage(spaced) = %q", got)
	}
	if got := SideCloseErrorMessage(rustThreadID1, errors.New(" transport disconnected ")); got != "Failed to close side conversation "+rustThreadID1+"; it is still open:  transport disconnected " {
		t.Fatalf("SideCloseErrorMessage(preserve spaces) = %q", got)
	}
}
