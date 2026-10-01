//go:build !windows

package appserverdaemon

import (
	"fmt"
	"os"
	"path/filepath"
)

// selectDaemonRelease publishes release as the daemon's `current` selection by
// swapping a symlink into place (Rust prepare_install's Unix branch).
func selectDaemonRelease(root, release string) error {
	temporary, err := os.MkdirTemp(root, stagingDirPrefix)
	if err != nil {
		return fmt.Errorf("failed to stage the daemon selection in %s: %w", root, err)
	}
	defer os.RemoveAll(temporary)
	link := filepath.Join(temporary, "current")
	if err := os.Symlink(release, link); err != nil {
		return fmt.Errorf("failed to link the daemon selection %s: %w", release, err)
	}
	if err := os.Rename(link, filepath.Join(root, "current")); err != nil {
		return fmt.Errorf("failed to publish the daemon selection in %s: %w", root, err)
	}
	return nil
}

// validateDaemonSelection is a Windows-only guard: off Windows the selection is
// a symlink inside the releases directory, which the swap replaces atomically
// (Rust gates windows::validate_selection the same way).
func validateDaemonSelection(string) error {
	return nil
}
