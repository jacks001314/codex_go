//go:build darwin

package sandboxpath

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMetadataCaseAliasDarwin(t *testing.T) {
	parent := t.TempDir()
	workspace := filepath.Join(parent, "workspace")
	if err := os.MkdirAll(filepath.Join(workspace, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(parent, "WORKSPACE")); err != nil {
		t.Skip("filesystem is case-sensitive")
	}
	got, ok := metadataCaseAlias(filepath.Join(workspace, ".GIT", "config"), workspace)
	if !ok || got != filepath.Join(workspace, ".git", "config") {
		t.Fatalf("metadataCaseAlias() = %q, %v", got, ok)
	}
	got, ok = metadataCaseAlias(filepath.Join(parent, "WORKSPACE", ".GIT", "config"), filepath.Join(parent, "WORKSPACE"))
	if !ok || got != filepath.Join(parent, "WORKSPACE", ".git", "config") {
		t.Fatalf("root-alias metadataCaseAlias() = %q, %v", got, ok)
	}
}
