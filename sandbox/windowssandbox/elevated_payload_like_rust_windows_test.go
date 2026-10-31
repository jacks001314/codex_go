//go:build windows

package windowssandbox

import (
	"testing"

	coresandbox "codex_go/sandbox"
)

// TestElevatedProvisioningPayloadContainsNoCallerACLRootsLikeRust pins Rust
// #42309: elevated interactive setup only carries sandbox identities and proxy
// settings, while the non-elevated refresh applies the caller's ACL roots.
func TestElevatedProvisioningPayloadContainsNoCallerACLRootsLikeRust(t *testing.T) {
	profile := coresandbox.WorkspaceWritePermissionProfile()
	permissions, err := ResolvePermissions(&profile, []string{`C:\workspace`})
	if err != nil {
		t.Fatalf("ResolvePermissions() error = %v", err)
	}
	codexHome := t.TempDir()
	payload := elevatedProvisioningPayload(&SandboxSetupRequest{
		CodexHome:   codexHome,
		CommandCWD:  `C:\workspace\repo`,
		Permissions: permissions,
	})

	if payload.CommandCWD != codexHome {
		t.Fatalf("payload.CommandCWD = %q, want %q", payload.CommandCWD, codexHome)
	}
	if len(payload.ReadRoots) != 0 || len(payload.WriteRoots) != 0 {
		t.Fatalf("payload roots = %#v / %#v, want none", payload.ReadRoots, payload.WriteRoots)
	}
	if len(payload.DenyReadPaths) != 0 || len(payload.DenyWritePaths) != 0 {
		t.Fatalf("payload deny paths = %#v / %#v, want none", payload.DenyReadPaths, payload.DenyWritePaths)
	}
	if payload.Mode != setupModeInteractiveProvision {
		t.Fatalf("payload.Mode = %q, want %q", payload.Mode, setupModeInteractiveProvision)
	}
}
