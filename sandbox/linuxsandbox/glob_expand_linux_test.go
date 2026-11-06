//go:build linux

package linuxsandbox

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/sandbox/sandboxpath"
)

// Rust #59711: deny globs expand in process, so hidden directories are covered
// and no external scanner has to exist on PATH.
func TestExpandUnreadableGlobsWalksInProcessLikeRust(t *testing.T) {
	root := t.TempDir()
	scanRoot := filepath.Join(root, "workspace")
	hiddenDir := filepath.Join(scanRoot, ".hidden")
	nestedDir := filepath.Join(scanRoot, "nested")
	for _, dir := range []string{hiddenDir, nestedDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{
		filepath.Join(scanRoot, "top.key"),
		filepath.Join(hiddenDir, "hidden.key"),
		filepath.Join(nestedDir, "nested.key"),
	} {
		if err := os.WriteFile(file, []byte("secret"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	patterns := []string{
		filepath.Join(scanRoot, "*.key"),
		filepath.Join(hiddenDir, "*.key"),
		filepath.Join(nestedDir, "*.key"),
	}
	paths, err := ExpandUnreadableGlobsInEnvironment(
		patterns,
		scanRoot,
		sandboxpath.FilesystemPolicy{},
		nil,
		map[string]string{"PATH": ""},
		"",
	)
	if err != nil {
		t.Fatalf("ExpandUnreadableGlobsInEnvironment() error = %v", err)
	}
	if len(paths) != 3 {
		t.Fatalf("expanded paths = %#v, want 3 matches", paths)
	}
	depth := 1
	shallow, err := ExpandUnreadableGlobsInEnvironment(
		patterns,
		scanRoot,
		sandboxpath.FilesystemPolicy{},
		&depth,
		map[string]string{"PATH": ""},
		"",
	)
	if err != nil {
		t.Fatalf("depth-limited expansion error = %v", err)
	}
	if len(shallow) != 1 || shallow[0] != filepath.Join(scanRoot, "top.key") {
		t.Fatalf("depth-limited paths = %#v, want the top-level match", shallow)
	}
}
