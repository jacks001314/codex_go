package app

import (
	"strings"
	"testing"

	"codex_go/cli"
	codextui "codex_go/tui"
	"codex_go/tui/chatwidget"
)

// TestInitialApproveForMePresetLikeRust mirrors Rust #63589: the
// `--approve-for-me` preset applies to the initial conversation only. A new
// thread/start carries on-request approvals, the auto reviewer and
// workspace-write; later threads of the same process fall back to the regular
// launch configuration.
func TestInitialApproveForMePresetLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	root := &cli.RootOptions{Shared: cli.SharedOptions{ApproveForMe: true}}
	state := codextui.NewState(&codextui.Options{CWD: t.TempDir()})

	initial := interactiveLocalThreadStartParams(root, state)
	if initial.ApprovalPolicy != "on-request" {
		t.Fatalf("initial approval policy = %#v, want on-request", initial.ApprovalPolicy)
	}
	if initial.ApprovalsReviewer == nil || *initial.ApprovalsReviewer != string(chatwidget.ApprovalsReviewerAutoReview) {
		t.Fatalf("initial approvals reviewer = %#v, want auto_review", initial.ApprovalsReviewer)
	}
	if initial.Sandbox != "workspace-write" {
		t.Fatalf("initial sandbox = %#v, want workspace-write", initial.Sandbox)
	}

	state.SetThreadID("thread-later")
	later := interactiveLocalThreadStartParams(root, state)
	if later.ApprovalPolicy == "on-request" {
		t.Fatalf("later approval policy = %#v, want the regular launch configuration", later.ApprovalPolicy)
	}
	if later.ApprovalsReviewer != nil {
		t.Fatalf("later approvals reviewer = %#v, want unset", later.ApprovalsReviewer)
	}
}

// TestApproveForMeStaysOutOfLaunchConfigurationLikeRust mirrors the other half
// of Rust #63589: the preset must not enter the launch configuration, so /cd and
// sidebar previews keep resolving the destination's own permissions.
func TestApproveForMeStaysOutOfLaunchConfigurationLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	root := &cli.RootOptions{Shared: cli.SharedOptions{ApproveForMe: true}}
	options := interactivePermissionLoadOptions(root, t.TempDir())
	if options == nil {
		t.Fatal("interactivePermissionLoadOptions returned nil")
	}
	for _, override := range options.RawOverrides {
		if strings.Contains(override, "auto_review") || strings.Contains(override, "approval_policy") ||
			strings.Contains(override, "sandbox_mode") {
			t.Fatalf("approve-for-me leaked into launch overrides: %#v", options.RawOverrides)
		}
	}
}
