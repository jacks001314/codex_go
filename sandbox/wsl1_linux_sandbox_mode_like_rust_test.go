package sandbox

import (
	"strings"
	"testing"

	"codex_go/sandbox/linuxsandbox"
)

// Mirrors Rust #62215: the effective Linux backend is resolved once, managed
// routes force bubblewrap, and WSL1 rejects every configuration that would need
// bubblewrap while keeping the legacy Landlock path available.
func TestEnsureLinuxSandboxModeSupportedOnWSL1LikeRust(t *testing.T) {
	readOnly := ReadOnlyPermissionProfile()
	workspaceWrite := WorkspaceWritePermissionProfile()
	fullDisk := PermissionProfile{Disabled: true}

	if linuxsandbox.ResolveLinuxSandboxMode(true, true).LegacyLandlock {
		t.Fatal("a managed route resolved to legacy Landlock")
	}
	if !linuxsandbox.ResolveLinuxSandboxMode(true, false).LegacyLandlock {
		t.Fatal("a plain legacy request did not resolve to legacy Landlock")
	}
	if linuxsandbox.ResolveLinuxSandboxMode(false, false).LegacyLandlock {
		t.Fatal("the default backend resolved to legacy Landlock")
	}

	bubblewrap := linuxsandbox.ResolveLinuxSandboxMode(false, false)
	legacy := linuxsandbox.ResolveLinuxSandboxMode(true, false)
	managed := linuxsandbox.ResolveLinuxSandboxMode(true, true)

	// Hosts other than WSL1 keep the existing behavior.
	for _, profile := range []*PermissionProfile{&readOnly, &workspaceWrite, &fullDisk} {
		for _, mode := range []linuxsandbox.LinuxSandboxMode{bubblewrap, legacy, managed} {
			if err := EnsureLinuxSandboxModeSupportedOnWSL1(mode, profile, false); err != nil {
				t.Fatalf("non-WSL1 host rejected mode %#v: %v", mode, err)
			}
		}
	}

	// WSL1 rejects bubblewrap when the filesystem is restricted.
	for _, profile := range []*PermissionProfile{&readOnly, &workspaceWrite} {
		err := EnsureLinuxSandboxModeSupportedOnWSL1(bubblewrap, profile, true)
		if err == nil || !strings.Contains(err.Error(), "WSL2") {
			t.Fatalf("WSL1 bubblewrap error = %v, want the bubblewrap warning", err)
		}
	}
	// A managed network route requires bubblewrap regardless of the legacy flag.
	for _, mode := range []linuxsandbox.LinuxSandboxMode{managed, linuxsandbox.ResolveLinuxSandboxMode(false, true)} {
		if err := EnsureLinuxSandboxModeSupportedOnWSL1(mode, &fullDisk, true); err == nil {
			t.Fatalf("WSL1 accepted a managed route for mode %#v", mode)
		}
	}
	// Bubblewrap with a full-disk policy and the legacy backend stay usable.
	if err := EnsureLinuxSandboxModeSupportedOnWSL1(bubblewrap, &fullDisk, true); err != nil {
		t.Fatalf("WSL1 rejected a full-disk bubblewrap launch: %v", err)
	}
	if err := EnsureLinuxSandboxModeSupportedOnWSL1(legacy, &readOnly, true); err != nil {
		t.Fatalf("WSL1 rejected the legacy Landlock path: %v", err)
	}
}
