package windowssandbox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProtectWorkspaceCodexDirSkipsMissingDir(t *testing.T) {
	added, err := ProtectWorkspaceCodexDir(t.TempDir(), "S-1-5-21-1-2-3-4")
	if err != nil {
		t.Fatalf("ProtectWorkspaceCodexDir() error = %v", err)
	}
	if added {
		t.Fatalf("ProtectWorkspaceCodexDir() added = true, want false for missing dir")
	}
}

func TestProtectWorkspaceAgentsDirSkipsFile(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, ".agents"), []byte("not a dir"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	added, err := ProtectWorkspaceAgentsDir(cwd, "S-1-5-21-1-2-3-4")
	if err != nil {
		t.Fatalf("ProtectWorkspaceAgentsDir() error = %v", err)
	}
	if added {
		t.Fatalf("ProtectWorkspaceAgentsDir() added = true, want false for file")
	}
}
