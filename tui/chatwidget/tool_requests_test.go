package chatwidget

import "testing"

func TestToolRequestToInterruptCarriesResolvedMetadataMatchRust(t *testing.T) {
	exec := ToolRequestToInterrupt(ToolRequest{
		Kind:       ToolRequestExecApproval,
		ID:         "request-1",
		CallID:     "call-1",
		ApprovalID: "approval-1",
	})
	if exec.Kind != QueuedInterruptExecApproval || exec.ID != "request-1" || exec.ApprovalID != "approval-1" {
		t.Fatalf("exec interrupt = %#v", exec)
	}

	mcp := ToolRequestToInterrupt(ToolRequest{
		Kind:       ToolRequestMcpElicitation,
		ID:         "request-2",
		ServerName: "server",
		RequestID:  "elicitation-1",
	})
	if mcp.Kind != QueuedInterruptElicitation || mcp.ServerName != "server" || mcp.RequestID != "elicitation-1" {
		t.Fatalf("mcp interrupt = %#v", mcp)
	}

	permissions := ToolRequestToInterrupt(ToolRequest{Kind: ToolRequestPermissions, CallID: "call-3"})
	if permissions.Kind != QueuedInterruptRequestPermissions || permissions.ID != "call-3" {
		t.Fatalf("permissions interrupt = %#v", permissions)
	}
}

func TestGuardianAssessmentInProgressAggregatesStatusMatchRust(t *testing.T) {
	state := ToolRequestRuntimeState{}

	first := state.OnGuardianAssessment(GuardianAssessmentEvent{
		ID:     "review-1",
		Status: GuardianAssessmentInProgress,
		Action: GuardianAssessmentAction{Kind: GuardianActionCommand, Command: "go test ./..."},
	})
	if !first.InProgress || !first.EnsureStatusIndicator || !first.InterruptHintVisible || first.Status == nil {
		t.Fatalf("first in-progress result = %#v", first)
	}
	if first.Status.Header != "Reviewing approval request" || first.Status.Details != "go test ./..." {
		t.Fatalf("first status = %#v", first.Status)
	}

	second := state.OnGuardianAssessment(GuardianAssessmentEvent{
		ID:     "review-2",
		Status: GuardianAssessmentInProgress,
		Action: GuardianAssessmentAction{Kind: GuardianActionNetworkAccess, Target: "api.example.com"},
	})
	if second.Status == nil || second.Status.Header != "Reviewing 2 approval requests" {
		t.Fatalf("second status = %#v", second.Status)
	}
	if second.Status.DetailsMaxLines != 4 {
		t.Fatalf("details max lines = %d", second.Status.DetailsMaxLines)
	}
}

func TestGuardianAssessmentTerminalMessagesAndDenialsMatchRust(t *testing.T) {
	state := ToolRequestRuntimeState{}
	state.OnGuardianAssessment(GuardianAssessmentEvent{
		ID:     "review-1",
		Status: GuardianAssessmentInProgress,
		Action: GuardianAssessmentAction{Kind: GuardianActionCommand, Command: "bash -lc \"go test\""},
	})

	denied := state.OnGuardianAssessment(GuardianAssessmentEvent{
		ID:     "review-1",
		Status: GuardianAssessmentDenied,
		Action: GuardianAssessmentAction{Kind: GuardianActionCommand, Command: "bash -lc \"go test\""},
	})
	if denied.HistoryMessage != "Request denied for codex to run go test" {
		t.Fatalf("denied history = %q", denied.HistoryMessage)
	}
	if denied.RecentAutoReviewDenial == nil || denied.RecentAutoReviewDenial.Summary != "bash -lc \"go test\"" {
		t.Fatalf("denial entry = %#v", denied.RecentAutoReviewDenial)
	}
	if len(state.RecentAutoReviewDenials) != 1 || !state.PendingGuardianReviewStatus.IsEmpty() {
		t.Fatalf("state after denial = %#v", state)
	}

	approved := GuardianAssessmentHistoryMessage(GuardianAssessmentEvent{
		Status: GuardianAssessmentApproved,
		Action: GuardianAssessmentAction{Kind: GuardianActionMcpToolCall, Server: "github", ToolName: "search"},
	})
	if approved != "" {
		t.Fatalf("approved history = %q, want empty (approved Guardian assessments are hidden, Rust #38032)", approved)
	}

	timedOut := GuardianAssessmentHistoryMessage(GuardianAssessmentEvent{
		Status: GuardianAssessmentTimedOut,
		Action: GuardianAssessmentAction{Kind: GuardianActionApplyPatch, Files: []string{"a.go", "b.go"}},
	})
	if timedOut != "Review timed out before codex could apply a patch touching 2 files" {
		t.Fatalf("timed out history = %q", timedOut)
	}
}

func TestGuardianActionSummaryAndRequestUserInputTitleMatchRust(t *testing.T) {
	cases := []struct {
		action GuardianAssessmentAction
		want   string
	}{
		{GuardianAssessmentAction{Kind: GuardianActionExecve, Program: "python", Argv: []string{"python", "server.py"}}, "python server.py"},
		{GuardianAssessmentAction{Kind: GuardianActionApplyPatch, Files: []string{"a.go"}}, "apply_patch touching a.go"},
		{GuardianAssessmentAction{Kind: GuardianActionMcpToolCall, Server: "figma", ConnectorName: "Figma", ToolName: "open"}, "MCP open on Figma"},
		{GuardianAssessmentAction{Kind: GuardianActionRequestPermissions, Reason: "need network"}, "permission request: need network"},
	}
	for _, tc := range cases {
		if got := GuardianActionSummary(tc.action); got != tc.want {
			t.Fatalf("summary = %q, want %q", got, tc.want)
		}
	}

	if got := RequestUserInputNotificationTitle([]ToolRequestQuestion{{Header: " Pick a branch ", Question: "Which branch?"}}); got != "Pick a branch" {
		t.Fatalf("single title = %q", got)
	}
	if got := RequestUserInputNotificationTitle([]ToolRequestQuestion{{Question: "Which branch?"}}); got != "Which branch?" {
		t.Fatalf("single fallback title = %q", got)
	}
	if got := RequestUserInputNotificationTitle([]ToolRequestQuestion{{}, {}}); got != "2 questions requested" {
		t.Fatalf("multi title = %q", got)
	}
}
