//go:build !windows

package appserver

import (
	"os"
	"testing"
)

// privateTempDir returns a short temporary directory the protected
// control-socket transport accepts on every platform.
//
// Two properties matter. testing.TempDir creates its per-call directory with
// 0o777 before the umask, so a host with the usual 022 umask gets 0o755 and the
// Rust #45984 parent check refuses it exactly as the Rust binary would.
// os.TempDir plus a two-letter name also keeps the advertised rendezvous path
// inside the AF_UNIX sun_path limit, which a test name used as the directory
// name exceeds on macOS.
func privateTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp(os.TempDir(), "cx")
	if err != nil {
		t.Fatalf("MkdirTemp() error = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("chmod %s = %v", dir, err)
	}
	return dir
}
