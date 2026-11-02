//go:build windows

package tui

import (
	"os"
	"syscall"
)

// fileAttributeReparsePoint is FILE_ATTRIBUTE_REPARSE_POINT.
const fileAttributeReparsePoint = 0x400

// pathTraversesReparsePoint reports whether the path or any ancestor is a
// Windows reparse point (junction, symlink, mount point). Rust checks parents
// before following any of them because a junction can lead to a UNC share.
func pathTraversesReparsePoint(path string) bool {
	for _, ancestor := range pathAncestorsRootFirst(path) {
		info, err := os.Lstat(ancestor)
		if err != nil {
			return true
		}
		data, ok := info.Sys().(*syscall.Win32FileAttributeData)
		if !ok {
			continue
		}
		if data.FileAttributes&fileAttributeReparsePoint != 0 {
			return true
		}
	}
	return false
}
