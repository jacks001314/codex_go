//go:build windows

package tool

// isSymlinkLoop reports the ELOOP an O_NOFOLLOW open raises for a symlinked
// copy source. Windows reparse points surface a different error, which the
// caller classifies through its errno table instead.
func isSymlinkLoop(error) bool {
	return false
}

// isNotDirectoryError reports ENOTDIR from a path whose ancestor is a regular
// file; Windows already surfaces that shape as a missing path.
func isNotDirectoryError(error) bool {
	return false
}
