package memories

import (
	"os"
	"path/filepath"
	"testing"
)

// Rust #56211: resetting a memory workspace that does not exist is a no-op, so
// a fresh V2 profile never materializes an unused V1 directory.
func TestClearRootsContentsLeavesAbsentWorkspaceAbsent(t *testing.T) {
	home := t.TempDir()
	if err := ClearRootsContents(home); err != nil {
		t.Fatalf("ClearRootsContents: %v", err)
	}
	for _, name := range []string{"memories", "memories_v2", "memories_extensions"} {
		if _, err := os.Stat(filepath.Join(home, name)); !os.IsNotExist(err) {
			t.Fatalf("%s exists after reset of an absent workspace (err=%v)", name, err)
		}
	}
}

// An existing workspace keeps its root directory but loses its contents.
func TestClearRootsContentsPreservesExistingRoot(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "memories")
	if err := os.MkdirAll(filepath.Join(root, "rollout_summaries"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "MEMORY.md"), []byte("stale\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := ClearRootsContents(home); err != nil {
		t.Fatalf("ClearRootsContents: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read root: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("root entries after reset = %v, want empty", entries)
	}
}
