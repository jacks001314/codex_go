package execserver

import (
	"path/filepath"
	"testing"
)

// canonicalTempDir returns a temporary directory with every symlinked ancestor
// resolved. No-follow operations reject a symlink in any component (Rust
// #39659), and a macOS temp path lives under /var, which is itself a symlink to
// /private/var; Rust's fixtures canonicalize for the same reason.
func canonicalTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s) error = %v", dir, err)
	}
	return resolved
}
