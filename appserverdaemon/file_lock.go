package appserverdaemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"codex_go/codexuds"
)

func openLockFile(path string) (*os.File, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("%w: lock file is empty", ErrDaemonPathsRequired)
	}
	// The lock file lives in the daemon state directory, which is also where the
	// updater serves its request socket. Create it with the platform's private
	// contract so both uses agree (Rust pid_start::start_inner).
	if err := codexuds.PreparePrivateSocketDirectory(filepath.Dir(path)); err != nil {
		return nil, fmt.Errorf("failed to create lock directory %s: %w", filepath.Dir(path), err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("failed to open lock file %s: %w", path, err)
	}
	// Windows LockFileEx cannot lock a zero-length range on an empty file, so
	// every lock file carries one byte regardless of the platform.
	if info, statErr := file.Stat(); statErr == nil && info.Size() == 0 {
		_, _ = file.WriteString("\n")
	}
	return file, nil
}
