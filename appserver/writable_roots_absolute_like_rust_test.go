package appserver

import (
	"strings"
	"testing"

	"codex_go/sandbox"
)

// Rust #62376: a writable root supplied over the wire must be absolute on the
// host that enforces the policy.
func TestTurnSandboxPolicyRejectsRelativeWritableRootLikeRust(t *testing.T) {
	_, _, err := turnSandboxPolicyPermissionProfile(map[string]any{
		"mode":          "workspace-write",
		"writableRoots": []string{"relative-root"},
	})
	if err == nil || err.Error() != "writable root is not absolute: relative-root" {
		t.Fatalf("turn policy error = %v", err)
	}

	absolute := t.TempDir()
	_, profile, err := turnSandboxPolicyPermissionProfile(map[string]any{
		"mode":          "workspace-write",
		"writableRoots": []string{absolute},
	})
	if err != nil {
		t.Fatalf("absolute writable root error = %v", err)
	}
	if profile == nil || profile.SandboxPolicy == nil || len(profile.SandboxPolicy.WritableRoots) != 1 ||
		profile.SandboxPolicy.WritableRoots[0] != absolute {
		t.Fatalf("profile = %#v", profile)
	}
}

func TestCommandExecRejectsRelativeWritableRootLikeRust(t *testing.T) {
	relative := &sandbox.SandboxPolicy{
		Kind:          sandbox.SandboxWorkspaceWrite,
		WritableRoots: []string{"relative-root"},
	}
	_, err := resolveCommandExecSandbox(&CommandExecParams{SandboxPolicy: relative}, t.TempDir(), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "writable root is not absolute: relative-root") {
		t.Fatalf("relative writable root error = %v", err)
	}

	absolute := t.TempDir()
	accepted := &sandbox.SandboxPolicy{
		Kind:          sandbox.SandboxWorkspaceWrite,
		WritableRoots: []string{absolute},
	}
	if _, err := resolveCommandExecSandbox(&CommandExecParams{SandboxPolicy: accepted}, t.TempDir(), nil, nil); err != nil {
		t.Fatalf("absolute writable root error = %v", err)
	}
}
