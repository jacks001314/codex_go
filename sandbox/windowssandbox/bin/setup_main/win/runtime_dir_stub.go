//go:build !windows

package win

import "os"

// isReparsePoint is a no-op off Windows; the runtime ACL path is unsupported
// there and the Lstat/IsDir eligibility check is sufficient.
func isReparsePoint(info os.FileInfo) bool {
	return false
}
