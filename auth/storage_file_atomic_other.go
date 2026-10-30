//go:build !windows

package auth

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// createAuthFileReplacement creates a 0600 temporary file next to the
// credential file so the rename keeps credential permissions (Rust #58251
// storage_file::replacement).
func createAuthFileReplacement(path string) (*os.File, error) {
	return os.CreateTemp(filepath.Dir(path), ".auth-")
}

// checkAuthFileLinks rejects hard-linked credential files: replacing one name
// would silently split the identity it shares with its peer (Rust #58251).
func checkAuthFileLinks(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	if stat.Nlink > 1 {
		return &os.PathError{Op: "replace", Path: path, Err: fmt.Errorf("atomic credential replacement does not support hard-linked files: %w", errors.ErrUnsupported)}
	}
	return nil
}
