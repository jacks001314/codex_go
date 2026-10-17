//go:build !unix

package cli

import "os"

// removeArg0DirLocked closes the session lock before deleting its directory so
// the lock file is never removed while still open (Rust #54739).
func removeArg0DirLocked(dir string, release func() error) error {
	if release != nil {
		if err := release(); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
