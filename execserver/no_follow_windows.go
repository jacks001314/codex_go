//go:build windows

package execserver

import (
	"os"

	"golang.org/x/sys/windows"
)

// noFollowComponentIsReparse reports whether a Windows path component is a
// reparse point. Go's Lstat reports junctions as irregular files rather than
// ModeSymlink, so the file attributes are the authoritative check. The DOS
// drive root itself is not a reparse point (Rust #51511).
func noFollowComponentIsReparse(path string, _ os.FileInfo) bool {
	attributes, err := windows.GetFileAttributes(windows.StringToUTF16Ptr(path))
	if err != nil {
		return false
	}
	return attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
}
