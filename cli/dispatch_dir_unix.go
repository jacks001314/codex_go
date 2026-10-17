//go:build unix

package cli

import (
	"os"
	"path/filepath"
)

// removeArg0DirLocked retires an arg0 session directory while its lock is
// still held, then closes the lock before deleting the renamed directory. The
// rename keeps initializers from publishing at the old path during cleanup and
// closing the lock first avoids NFS silly-rename entries (Rust #54739).
func removeArg0DirLocked(dir string, release func() error) error {
	retiredDir, err := os.MkdirTemp(filepath.Dir(dir), "codex-arg0-retired")
	if err != nil {
		return err
	}
	if err := os.Rename(dir, retiredDir); err != nil {
		_ = os.RemoveAll(retiredDir)
		return err
	}
	if release != nil {
		if err := release(); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(retiredDir); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
