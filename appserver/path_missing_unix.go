//go:build !windows

package appserver

import (
	"errors"
	"os"
	"syscall"
)

// isMissingPathError reports a path that cannot carry a config file: the file
// is missing, or an ancestor is a regular file (ENOTDIR). Rust #63088 treats
// NotADirectory like NotFound when reading the root checkout's project config
// from a linked worktree, so a ".codex" file must not warn or abort discovery.
func isMissingPathError(err error) bool {
	return os.IsNotExist(err) || errors.Is(err, syscall.ENOTDIR)
}
