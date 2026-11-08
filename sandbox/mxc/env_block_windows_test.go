//go:build windows

package mxc

import (
	"strings"
	"testing"
	"unicode/utf16"
)

// TestBuildEnvironmentBlockKeepsHostReservedValues locks the MXC launcher
// contract: an explicit child environment keeps the host's SystemDrive and
// LOCALAPPDATA values (caller overrides are replaced), because PSEC rejects a
// block that omits them.
func TestBuildEnvironmentBlockKeepsHostReservedValues(t *testing.T) {
	t.Setenv("SystemDrive", "C:")
	t.Setenv("LOCALAPPDATA", `C:\Users\codex\AppData\Local`)
	block, err := buildEnvironmentBlock([]string{"PATHEXT=.COM", "SystemDrive=D:", "LOCALAPPDATA=C:\\caller"})
	if err != nil {
		t.Fatalf("buildEnvironmentBlock() error = %v", err)
	}
	decoded := string(utf16.Decode(block))
	if strings.Contains(decoded, "D:") || strings.Contains(decoded, `C:\caller`) {
		t.Fatalf("caller overrides survived: %q", decoded)
	}
	if !strings.Contains(decoded, "SystemDrive=C:") || !strings.Contains(decoded, `LOCALAPPDATA=C:\Users\codex\AppData\Local`) {
		t.Fatalf("host values missing: %q", decoded)
	}
	if !strings.HasSuffix(decoded, "\x00\x00") {
		t.Fatalf("block is not double-NUL terminated: %q", decoded)
	}
}

// TestBuildEnvironmentBlockRequiresHostReservedValues locks the fail-closed
// diagnostic when the host itself cannot provide a reserved value.
func TestBuildEnvironmentBlockRequiresHostReservedValues(t *testing.T) {
	t.Setenv("LOCALAPPDATA", "")
	_, err := buildEnvironmentBlock([]string{"PATHEXT=.COM"})
	if err == nil || !strings.Contains(err.Error(), "MXC requires the host LOCALAPPDATA environment variable") {
		t.Fatalf("error = %v", err)
	}
}
