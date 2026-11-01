package appserver

import (
	"path/filepath"
	"testing"
)

// canonicalTempDir returns a temporary directory with every symlinked ancestor
// resolved. Discovered skill and instruction paths are canonical (Rust
// 72d937ed4d) and macOS reports its temporary directory through the /var
// symlink, so fixtures must compare against the canonical spelling.
func canonicalTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s) error = %v", dir, err)
	}
	return resolved
}
