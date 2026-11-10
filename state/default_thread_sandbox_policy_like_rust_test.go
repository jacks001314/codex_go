package state

import (
	"testing"

	"codex_go/sandbox"
)

// Rust #60472 stores the default thread permissions as a serialized
// `PermissionProfile::read_only()` instead of the legacy "read-only" tag, and
// keeps accepting legacy sandbox policies written by older clients.
func TestDefaultThreadSandboxPolicySerializesReadOnlyProfileLikeRust(t *testing.T) {
	encoded := defaultThreadSandboxPolicy()
	if encoded == "read-only" {
		t.Fatal("defaultThreadSandboxPolicy() = legacy tag, want a serialized permission profile")
	}
	profile, err := sandbox.ParseRuntimePermissionProfileJSON(encoded)
	if err != nil {
		t.Fatalf("ParseRuntimePermissionProfileJSON(%q) error = %v", encoded, err)
	}
	if profile == nil || profile.Disabled {
		t.Fatalf("parsed default profile = %#v, want a managed read-only profile", profile)
	}
	if profile.SandboxPolicy == nil || profile.SandboxPolicy.Kind != sandbox.SandboxReadOnly {
		t.Fatalf("parsed default sandbox policy = %#v, want read-only", profile.SandboxPolicy)
	}
	if profile.AllowsNetwork() {
		t.Fatal("default read-only profile allows network")
	}

	// Legacy values written by older clients still resolve.
	for _, legacy := range []string{"read-only", "danger-full-access"} {
		mode, err := sandbox.ParseSandboxMode(legacy)
		if err != nil {
			t.Fatalf("ParseSandboxMode(%q) error = %v", legacy, err)
		}
		policy, err := sandbox.SandboxPolicyFromMode(mode)
		if err != nil || policy == nil {
			t.Fatalf("SandboxPolicyFromMode(%q) = %#v, %v", legacy, policy, err)
		}
		if legacy == "danger-full-access" && !policy.HasFullDiskWriteAccess() {
			t.Fatalf("legacy danger-full-access policy = %#v, want full disk write", policy)
		}
	}
}
