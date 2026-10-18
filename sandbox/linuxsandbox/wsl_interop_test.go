//go:build linux

package linuxsandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyLandlockRejectsWSLInteropWithNetworkLikeRust(t *testing.T) {
	interop := filepath.Join(t.TempDir(), "WSL")
	if err := os.Mkdir(interop, 0o755); err != nil {
		t.Fatalf("Mkdir(WSL) error = %v", err)
	}
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
	err := ensureLegacyLandlockSupportsPolicyWithInterop(command, interop)
	if err == nil || !strings.Contains(err.Error(), "legacy Landlock cannot isolate WSL Windows interop") {
		t.Fatalf("legacy policy error = %v", err)
	}

	command.AllowNetworkForProxy = true
	if err := ensureLegacyLandlockSupportsPolicyWithInterop(command, interop); err != nil {
		t.Fatalf("managed proxy policy error = %v", err)
	}
	command.AllowNetworkForProxy = false
	command.PermissionProfile.NetworkEnabled = false
	if err := ensureLegacyLandlockSupportsPolicyWithInterop(command, interop); err != nil {
		t.Fatalf("restricted network policy error = %v", err)
	}
	command.PermissionProfile.NetworkEnabled = true
	command.PermissionProfile.Filesystem = &linuxFilesystemPolicy{Kind: "unrestricted"}
	if err := ensureLegacyLandlockSupportsPolicyWithInterop(command, interop); err != nil {
		t.Fatalf("full-disk policy error = %v", err)
	}
	if err := ensureLegacyLandlockSupportsPolicyWithInterop(command, filepath.Join(t.TempDir(), "missing")); err != nil {
		t.Fatalf("missing interop policy error = %v", err)
	}
}
