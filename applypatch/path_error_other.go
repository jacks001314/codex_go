//go:build windows

package applypatch

// isNotDirectory reports ENOTDIR from a component walk; Windows surfaces the
// same shape as a missing path, so the walk already continues there.
func isNotDirectory(error) bool {
	return false
}
