package mcp

import (
	"context"
	"testing"

	"codex_go/sandbox"
	"codex_go/tool"
)

// TestMCPApprovalCancellationReasonParsingLikeRust pins the MCP approval
// metadata contract of Rust #54679: the reason is read from
// `codex_approval_cancellation_reason`, an absent or unknown value means the
// client sent no provenance, and only the three known values classify.
func TestMCPApprovalCancellationReasonParsingLikeRust(t *testing.T) {
	if MCPApprovalCancellationReasonKey != "codex_approval_cancellation_reason" {
		t.Fatalf("reason key = %q", MCPApprovalCancellationReasonKey)
	}
	for _, testCase := range []struct {
		name     string
		meta     any
		want     MCPApprovalCancellationReason
		wantSeen bool
	}{
		{name: "user", meta: map[string]any{MCPApprovalCancellationReasonKey: "user"}, want: MCPApprovalCancellationUser, wantSeen: true},
		{name: "interrupted", meta: map[string]any{MCPApprovalCancellationReasonKey: "interrupted"}, want: MCPApprovalCancellationInterrupted, wantSeen: true},
		{name: "non-interactive", meta: map[string]any{MCPApprovalCancellationReasonKey: "non_interactive"}, want: MCPApprovalCancellationNonInteractive, wantSeen: true},
		{name: "trimmed", meta: map[string]any{MCPApprovalCancellationReasonKey: "  interrupted "}, want: MCPApprovalCancellationInterrupted, wantSeen: true},
		{name: "absent", meta: map[string]any{"persist": "session"}},
		{name: "no meta", meta: nil},
		{name: "unknown value", meta: map[string]any{MCPApprovalCancellationReasonKey: "timeout"}},
		{name: "non-string value", meta: map[string]any{MCPApprovalCancellationReasonKey: 7}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			reason, ok := ParseMCPApprovalCancellationReason(testCase.meta)
			if ok != testCase.wantSeen || reason != testCase.want {
				t.Fatalf("ParseMCPApprovalCancellationReason() = (%q, %v), want (%q, %v)", reason, ok, testCase.want, testCase.wantSeen)
			}
		})
	}
}

// TestMCPToolApprovalCancellationMessagesLikeRust mirrors the abort-path message
// choice of Rust #54679: a user action or missing provenance keeps the existing
// cancellation text, an interruption reports the pre-approval cancellation, and
// a non-interactive runtime explains why approval was unavailable.
func TestMCPToolApprovalCancellationMessagesLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		reason MCPApprovalCancellationReason
		want   string
	}{
		{reason: "", want: "user cancelled MCP tool call"},
		{reason: MCPApprovalCancellationUser, want: "user cancelled MCP tool call"},
		{reason: MCPApprovalCancellationInterrupted, want: "MCP tool call cancelled before approval"},
		{reason: MCPApprovalCancellationNonInteractive, want: "MCP tool call cancelled: interactive approval is unavailable in exec mode"},
	} {
		if got := MCPToolApprovalCancellationMessage(testCase.reason); got != testCase.want {
			t.Fatalf("message for %q = %q, want %q", testCase.reason, got, testCase.want)
		}
	}
}

// TestToolApprovalReportsCancellationProvenanceLikeRust pins the executor half of
// #54679: the abort output uses the provenance the handler recorded, and a
// rejection keeps its own message.
func TestToolApprovalReportsCancellationProvenanceLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		outcome MCPToolApprovalOutcome
		want    string
	}{
		{
			name:    "user",
			outcome: MCPToolApprovalOutcome{Decision: MCPToolApprovalDeny, CancellationReason: MCPApprovalCancellationUser},
			want:    "user cancelled MCP tool call",
		},
		{
			name:    "interrupted",
			outcome: MCPToolApprovalOutcome{Decision: MCPToolApprovalDeny, CancellationReason: MCPApprovalCancellationInterrupted},
			want:    "MCP tool call cancelled before approval",
		},
		{
			name:    "non-interactive",
			outcome: MCPToolApprovalOutcome{Decision: MCPToolApprovalDeny, CancellationReason: MCPApprovalCancellationNonInteractive},
			want:    "MCP tool call cancelled: interactive approval is unavailable in exec mode",
		},
		{
			name:    "client without provenance",
			outcome: MCPToolApprovalOutcome{Decision: MCPToolApprovalDeny},
			want:    "user cancelled MCP tool call",
		},
		{
			name:    "rejection keeps its own message",
			outcome: MCPToolApprovalOutcome{Decision: MCPToolApprovalReject, CancellationReason: MCPApprovalCancellationInterrupted},
			want:    "user rejected MCP tool call",
		},
		{
			name: "handler message wins",
			outcome: MCPToolApprovalOutcome{
				Decision:           MCPToolApprovalDeny,
				CancellationReason: MCPApprovalCancellationInterrupted,
				Message:            MCPElicitationHandoffMessage,
			},
			want: MCPElicitationHandoffMessage,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			executor := NewToolExecutor(&ToolExecutorOptions{
				Service:    NewMCPService(nil),
				ServerName: "docs",
				ToolName:   tool.PlainName("search"),
				ToolInfo:   &MCPToolInfo{Name: "search", Annotations: map[string]any{"destructiveHint": true}},
				ThreadID:   "thread-1",
				TurnID:     "turn-1",
				ToolApproval: &ToolApprovalOptions{
					ApprovalPolicy: sandbox.ApprovalOnRequest,
					Handler: MCPToolApprovalHandlerFunc(func(
						context.Context, *MCPToolApprovalRequest,
					) (MCPToolApprovalOutcome, error) {
						return testCase.outcome, nil
					}),
				},
			})
			denied, err := executor.approveToolCallIfNeeded(context.Background(), "call-1", map[string]any{}, "")
			if err != nil {
				t.Fatalf("approveToolCallIfNeeded() error = %v", err)
			}
			if denied == nil {
				t.Fatal("the aborted call produced no output")
			}
			if denied.Body != testCase.want || denied.Error != testCase.want {
				t.Fatalf("output = %#v, want %q", denied, testCase.want)
			}
		})
	}
}

// TestMCPToolApprovalResponseCancelsLikeRust pins the answer check that turns a
// user-input cancel into the user provenance (#54679).
func TestMCPToolApprovalResponseCancelsLikeRust(t *testing.T) {
	const questionID = "mcp_tool_call_approval_call-1"
	if MCPToolApprovalResponseCancels(nil, questionID) {
		t.Fatal("a missing response must not count as a user cancellation")
	}
	if MCPToolApprovalResponseCancels(&tool.UserInputResponse{Answers: map[string]string{questionID: MCPToolApprovalAccept}}, questionID) {
		t.Fatal("an approval must not count as a user cancellation")
	}
	if !MCPToolApprovalResponseCancels(&tool.UserInputResponse{Answers: map[string]string{questionID: MCPToolApprovalCancel}}, questionID) {
		t.Fatal("the cancel option must count as a user cancellation")
	}
	if !MCPToolApprovalResponseCancels(&tool.UserInputResponse{StructuredAnswers: map[string][]string{questionID: {MCPToolApprovalCancel}}}, questionID) {
		t.Fatal("a structured cancel answer must count as a user cancellation")
	}
	if MCPToolApprovalResponseCancels(&tool.UserInputResponse{Answers: map[string]string{"other": MCPToolApprovalCancel}}, questionID) {
		t.Fatal("another question's cancel must not count")
	}
}
