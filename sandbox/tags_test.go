package sandbox

import (
	"testing"

	"codex_go/safety"
)

func TestPermissionProfileTags(t *testing.T) {
	if got := PermissionProfilePolicyTag(safety.PermissionDisabled, safety.FileSystemPolicy{}, ""); got != "danger-full-access" {
		t.Fatalf("policy tag = %q", got)
	}
	tag := PermissionProfileSandboxTag(safety.PermissionManaged, safety.FileSystemPolicy{}, true, WindowsSandboxElevated, "landlock")
	if tag != "windows_elevated" {
		t.Fatalf("sandbox tag = %q", tag)
	}
	if got := PermissionProfileSandboxTag(safety.PermissionExternal, safety.FileSystemPolicy{}, false, WindowsSandboxDefault, "landlock"); got != "external" {
		t.Fatalf("external tag = %q", got)
	}
}

func TestSandboxPolicyTag(t *testing.T) {
	if got := SandboxPolicyTag(NewDangerFullAccessPolicy(), ""); got != "danger-full-access" {
		t.Fatalf("tag = %q", got)
	}
	if got := SandboxPolicyTag(NewWorkspaceWritePolicy(), "/repo"); got != "workspace-write" {
		t.Fatalf("tag = %q", got)
	}
}
