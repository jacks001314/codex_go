//go:build !windows

package appserver

import (
	"os"
	"testing"
)

// privateTempDir returns a temporary directory the protected control-socket
// checks accept. testing.TempDir creates its per-call directory with 0o777
// before the umask, so a host with the usual 022 umask gets 0o755 and the
// Rust #45984 parent check refuses it exactly as the Rust binary would.
func privateTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("chmod %s = %v", dir, err)
	}
	return dir
}

// shortTempDir returns a temporary directory whose absolute path stays inside
// the AF_UNIX sun_path limit. A test name used as the directory name easily
// exceeds it on macOS, where os.TempDir already spends most of the budget.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp(os.TempDir(), "cx")
	if err != nil {
		t.Fatalf("MkdirTemp() error = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
