//go:build linux

package linuxsandbox

import (
	"strings"
	"testing"
)

func TestLegacyLandlockRequiresBubblewrapForRestrictedFilesystemLikeRust(t *testing.T) {
	readOnly := &linuxFilesystemPolicy{
		Kind: "restricted",
		Entries: []linuxFilesystemEntry{{
			Access:  "read",
			Special: linuxSpecialPath{Kind: "root"},
		}},
	}
	command := &linuxSandboxCommand{
		UseLegacyLandlock: true,
		PermissionProfile: &linuxPermissionProfile{Filesystem: readOnly, NetworkEnabled: true},
	}
	err := ensureLegacyLandlockSupportsPolicy(command)
	if err == nil || !strings.Contains(err.Error(), "filesystem-restricted execution requires bubblewrap") {
		t.Fatalf("legacy policy error = %v", err)
	}
	// Network handling does not change the requirement.
	command.AllowNetworkForProxy = true
	if err := ensureLegacyLandlockSupportsPolicy(command); err == nil {
		t.Fatal("managed proxy policy error = nil, want the bubblewrap requirement")
	}
	command.AllowNetworkForProxy = false
	command.PermissionProfile.NetworkEnabled = false
	if err := ensureLegacyLandlockSupportsPolicy(command); err == nil {
		t.Fatal("restricted network policy error = nil, want the bubblewrap requirement")
	}
	// The full-disk policy still runs under legacy Landlock.
	command.PermissionProfile.NetworkEnabled = true
	command.PermissionProfile.Filesystem = &linuxFilesystemPolicy{Kind: "unrestricted"}
	if err := ensureLegacyLandlockSupportsPolicy(command); err != nil {
		t.Fatalf("full-disk policy error = %v", err)
	}
	// Bubblewrap runs need no legacy gate at all.
	command.UseLegacyLandlock = false
	command.PermissionProfile.Filesystem = readOnly
	if err := ensureLegacyLandlockSupportsPolicy(command); err != nil {
		t.Fatalf("bubblewrap policy error = %v", err)
	}
}
