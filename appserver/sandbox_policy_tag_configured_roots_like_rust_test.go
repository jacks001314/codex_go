package appserver

import (
	"path/filepath"
	"testing"

	"codex_go/safety"
	"codex_go/sandbox"
)

// TestSandboxPolicyTagsUseConfiguredRootsLikeRust mirrors Rust #41933: the
// diagnostic sandbox policy label derives from configured writable roots and
// never inspects the filesystem, so a root that does not exist yet still labels
// the policy workspace-write. Runtime authorization keeps its own resolution.
func TestSandboxPolicyTagsUseConfiguredRootsLikeRust(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-created", "project")

	policy := sandbox.NewWorkspaceWritePolicy()
	policy.WritableRoots = []string{missing}
	if got := sandbox.SandboxPolicyTag(policy, missing); got != "workspace-write" {
		t.Fatalf("SandboxPolicyTag(missing configured root) = %q, want workspace-write", got)
	}
	if got := sandbox.PermissionProfilePolicyTag(safety.PermissionManaged, safety.FileSystemPolicy{WritableRoots: []string{missing}}, missing); got != "workspace-write" {
		t.Fatalf("PermissionProfilePolicyTag(missing configured root) = %q, want workspace-write", got)
	}

	profile := sandbox.WorkspaceWritePermissionProfile()
	profile.SandboxPolicy.WritableRoots = []string{missing}
	if got := permissionProfilePolicyTagFromProfile(&profile, missing); got != "workspace-write" {
		t.Fatalf("permissionProfilePolicyTagFromProfile(missing configured root) = %q, want workspace-write", got)
	}

	if got := sandbox.SandboxPolicyTag(sandbox.NewReadOnlyPolicy(), missing); got != "read-only" {
		t.Fatalf("read-only tag = %q", got)
	}
	if got := sandbox.SandboxPolicyTag(sandbox.NewDangerFullAccessPolicy(), missing); got != "danger-full-access" {
		t.Fatalf("danger-full-access tag = %q", got)
	}
}
