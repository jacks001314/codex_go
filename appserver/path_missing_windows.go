//go:build windows

package appserver

import "os"

// isMissingPathError reports a path that cannot carry a config file. Windows
// surfaces a regular file where a directory is expected as a missing path, so
// IsNotExist already covers the NotADirectory shape (Rust #63088).
func isMissingPathError(err error) bool {
	return os.IsNotExist(err)
}
