//go:build windows

package windowssandbox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProtectWorkspaceCodexDirAddsDenyWrite(t *testing.T) {
	cwd := t.TempDir()
	path := filepath.Join(cwd, ".gcode")
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	added, err := ProtectWorkspaceCodexDir(cwd, testCapabilitySID)
	if err != nil {
		t.Fatalf("ProtectWorkspaceCodexDir() error = %v", err)
	}
	if !added {
		t.Fatalf("ProtectWorkspaceCodexDir() added = false, want true")
	}
	assertDACLHasACE(t, path, testCapabilitySID, aclACEKindDenyWrite)
}
