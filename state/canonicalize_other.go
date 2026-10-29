//go:build !windows

package state

import "path/filepath"

// canonicalizePath resolves every link on the way to path, matching Rust's
// std::fs::canonicalize for the managed rollout roots (Rust #42135).
func canonicalizePath(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}
