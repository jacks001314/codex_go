package appserver

import (
	"testing"

	"codex_go/config"
	"codex_go/sandbox"
)

// TestPermissionProfilePolicyTagLikeRust mirrors Rust's permission_profile_policy_tag
// coverage (codex-rs/core/src/sandbox_tags_tests.rs, Rust 4ca25a2c4e): the
// `sandbox_mode` turn-metadata value derives from the effective permission
// profile and the working directory.
func TestPermissionProfilePolicyTagLikeRust(t *testing.T) {
	workspace := WorkspaceWritePermissionProfileResolution(t, "C:\\repo")
	readOnly := ReadOnlyPermissionProfileResolution(t)
	fullAccess := FullAccessPermissionProfileResolution(t)

	if got := permissionProfilePolicyTag(workspace, "C:\\repo"); got != "workspace-write" {
		t.Fatalf("workspace-write tag = %q, want workspace-write", got)
	}
	if got := permissionProfilePolicyTag(readOnly, "C:\\repo"); got != "read-only" {
		t.Fatalf("read-only tag = %q, want read-only", got)
	}
	if got := permissionProfilePolicyTag(fullAccess, "C:\\repo"); got != "danger-full-access" {
		t.Fatalf("full-access tag = %q, want danger-full-access", got)
	}
	if got := permissionProfilePolicyTag(nil, "C:\\repo"); got != "danger-full-access" {
		t.Fatalf("nil tag = %q, want danger-full-access", got)
	}
	profile := sandbox.WorkspaceWritePermissionProfile()
	if got := permissionProfilePolicyTagFromProfile(&profile, "C:\\repo"); got != "workspace-write" {
		t.Fatalf("from-profile tag = %q, want workspace-write", got)
	}
	if got := permissionProfilePolicyTagFromProfile(nil, "C:\\repo"); got != "danger-full-access" {
		t.Fatalf("nil profile tag = %q, want danger-full-access", got)
	}
}

func WorkspaceWritePermissionProfileResolution(t *testing.T, cwd string) *config.SandboxPermissionProfileResolution {
	t.Helper()
	profile := sandbox.WorkspaceWritePermissionProfile()
	return &config.SandboxPermissionProfileResolution{ID: "workspace", Profile: &profile}
}

func ReadOnlyPermissionProfileResolution(t *testing.T) *config.SandboxPermissionProfileResolution {
	t.Helper()
	profile := sandbox.ReadOnlyPermissionProfile()
	return &config.SandboxPermissionProfileResolution{ID: "read-only", Profile: &profile}
}

func FullAccessPermissionProfileResolution(t *testing.T) *config.SandboxPermissionProfileResolution {
	t.Helper()
	profile := sandbox.FullAccessPermissionProfile()
	return &config.SandboxPermissionProfileResolution{ID: "danger-full-access", Profile: &profile}
}
