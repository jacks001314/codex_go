//go:build windows

package win

import (
	"os"
	"syscall"
)

// isReparsePoint reports whether the file info describes a directory reparse
// point (junction or symlink). Mirrors Rust's FILE_ATTRIBUTE_REPARSE_POINT
// guard in setup_runtime_bin.rs (#38064): reparse-point directories are
// skipped so ACL inheritance is not applied through a link.
func isReparsePoint(info os.FileInfo) bool {
	if info == nil {
		return false
	}
	attributes, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return false
	}
	return attributes.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0
}
