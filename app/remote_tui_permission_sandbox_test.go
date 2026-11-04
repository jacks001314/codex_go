package app

import (
	"testing"

	"codex_go/cli"
	codextui "codex_go/tui"
	"codex_go/tui/chatwidget"
	codextea "codex_go/tui/tea"
)

// TestRemotePermissionSendsDaemonSandboxMode covers the daemon contract: the
// TUI keeps permission profile ids in its session state, while thread/start,
// thread/fork and turn/start send the plain sandbox mode the daemon parses.
func TestRemotePermissionSendsDaemonSandboxMode(t *testing.T) {
	cases := []struct {
		profile string
		want    string
	}{
		{chatwidget.ReadOnlyProfile, "read-only"},
		{chatwidget.WorkspaceProfile, "workspace-write"},
		{chatwidget.DangerFullAccessProfile, "danger-full-access"},
		{"read-only", "read-only"},
		{"workspace-write", "workspace-write"},
	}
	for _, tc := range cases {
		if got := remoteSandboxMode(tc.profile); got != tc.want {
			t.Fatalf("remoteSandboxMode(%q) = %#v, want %q", tc.profile, got, tc.want)
		}
	}
	if got := remoteSandboxMode(""); got != nil {
		t.Fatalf("remoteSandboxMode(\"\") = %#v, want nil", got)
	}

	t.Setenv("CODEX_HOME", t.TempDir())
	state := codextui.NewState(nil)
	state.Sandbox = chatwidget.ReadOnlyProfile
	params, err := remoteTurnStartParams(&cli.RootOptions{}, state, "thread-sandbox", codextea.SubmitRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("remoteTurnStartParams() error = %v", err)
	}
	if params.SandboxPolicy != "read-only" {
		t.Fatalf("turn/start sandboxPolicy = %#v, want read-only", params.SandboxPolicy)
	}
	start, err := remoteThreadStartParams(&cli.RootOptions{}, state)
	if err != nil {
		t.Fatalf("remoteThreadStartParams() error = %v", err)
	}
	if start.Sandbox != "read-only" {
		t.Fatalf("thread/start sandbox = %#v, want read-only", start.Sandbox)
	}

	// The reviewer is part of the request snapshots, so "Approve for me"
	// survives the next turn instead of falling back to the user reviewer.
	state.ApprovalsReviewer = "auto_review"
	params, err = remoteTurnStartParams(&cli.RootOptions{}, state, "thread-reviewer", codextea.SubmitRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("remoteTurnStartParams() error = %v", err)
	}
	if params.ApprovalsReviewer == nil || *params.ApprovalsReviewer != "auto_review" {
		t.Fatalf("turn/start approvalsReviewer = %#v, want auto_review", params.ApprovalsReviewer)
	}
	start, err = remoteThreadStartParams(&cli.RootOptions{}, state)
	if err != nil {
		t.Fatalf("remoteThreadStartParams() error = %v", err)
	}
	if start.ApprovalsReviewer == nil || *start.ApprovalsReviewer != "auto_review" {
		t.Fatalf("thread/start approvalsReviewer = %#v, want auto_review", start.ApprovalsReviewer)
	}
}
