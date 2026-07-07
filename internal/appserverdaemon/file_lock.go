package appserverdaemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func openLockFile(path string) (*os.File, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("%w: lock file is empty", ErrDaemonPathsRequired)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("failed to create lock directory %s: %w", filepath.Dir(path), err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("failed to open lock file %s: %w", path, err)
	}
	return file, nil
}
