package envutil

import (
	"runtime"
	"testing"
)

// Rust #61724 separates the Linux-only helper lookup from the full trusted
// lookup so a distro adapter is not confused with WSL's Windows executables.
func TestLinuxTrustedExecutableIsLinuxOnly(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("the Linux lookup is exercised on Linux hosts")
	}
	if path, ok := LinuxTrustedExecutable("powershell.exe"); ok {
		t.Fatalf("LinuxTrustedExecutable(%q) = %q on %s, want false", "powershell.exe", path, runtime.GOOS)
	}
}