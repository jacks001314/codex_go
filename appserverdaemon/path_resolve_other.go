//go:build !windows

package appserverdaemon

import "path/filepath"

// resolveFinalPath resolves every link on the way to path, matching Rust's
// canonicalize on Unix.
func resolveFinalPath(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}
