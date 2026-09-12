package app

import (
	"errors"
	"testing"

	"codex_go/appserver"
	"codex_go/sandbox"
)

// TestRemoteTUIResumeConflictDetection covers Rust #43253's external-writer
// detection: only the active-writer conflict falls back to a read-only
// snapshot.
func TestRemoteTUIResumeConflictDetection(t *testing.T) {
	if !remoteTUIResumeConflict(errors.New("conflict: thread thread-1 already has an active writer")) {
		t.Fatal("the active-writer conflict must be recognized")
	}
	if remoteTUIResumeConflict(errors.New("thread not found")) {
		t.Fatal("unrelated errors must propagate")
	}
	if remoteTUIResumeConflict(nil) {
		t.Fatal("a successful resume is not a conflict")
	}
}

// TestRemoteTUISettingsFromResume covers the resume response's thread settings
// mapping (Rust #43330/#43340).
func TestRemoteTUISettingsFromResume(t *testing.T) {
	effort := "high"
	tier := "flex"
	reviewer := "user"
	settings := remoteTUISettingsFromResume(&appserver.ThreadResumeResponse{
		CWD:                     " D:/repo ",
		Model:                   "server-model",
		ModelProvider:           "server-provider",
		ApprovalPolicy:          "on-request",
		ApprovalsReviewer:       &reviewer,
		Sandbox:                 "workspace-write",
		ReasoningEffort:         &effort,
		ServiceTier:             &tier,
		ActivePermissionProfile: &sandbox.ActivePermissionProfile{ID: "trusted"},
		DisabledPluginIDs:       []string{"plugin-1"},
		RuntimeWorkspaceRoots:   []string{"D:/repo"},
	})
	if settings == nil {
		t.Fatal("settings must be produced")
	}
	if settings.CWD != "D:/repo" || settings.Model != "server-model" || settings.ModelProvider != "server-provider" {
		t.Fatalf("settings = %#v", settings)
	}
	if settings.ApprovalPolicy != "on-request" || settings.ApprovalsReviewer != "user" || settings.SandboxPolicy != "workspace-write" {
		t.Fatalf("permissions = %#v", settings)
	}
	if settings.Effort == nil || *settings.Effort != "high" || settings.ServiceTier == nil || *settings.ServiceTier != "flex" {
		t.Fatalf("optional settings = %#v", settings)
	}
	if settings.ActivePermissionProfile == nil || *settings.ActivePermissionProfile != "trusted" {
		t.Fatalf("active profile = %#v", settings.ActivePermissionProfile)
	}
	if len(settings.DisabledPluginIDs) != 1 || len(settings.RuntimeWorkspaceRoots) != 1 {
		t.Fatalf("lists = %#v / %#v", settings.DisabledPluginIDs, settings.RuntimeWorkspaceRoots)
	}
	if remoteTUISettingsFromResume(nil) != nil {
		t.Fatal("nil resume response must produce nil settings")
	}
}
