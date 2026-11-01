//go:build windows

package execserver

import (
	"errors"
	"os"

	"codex_go/shell"
)

// Shell snapshots are unsupported on Windows (shellSnapshotPlatformSupported),
// so the unnamed-file transport has no implementation there (Rust #48078 is
// Unix-only as well).
func materializeShellSnapshotTransport(shell.ShellType, string) (*os.File, error) {
	return nil, errors.New("shell snapshot descriptor replay is unavailable on this platform")
}
