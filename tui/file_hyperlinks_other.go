//go:build !windows

package tui

// pathTraversesReparsePoint has no reparse points to reject off Windows;
// EvalSymlinks still resolves the path before the second validation.
func pathTraversesReparsePoint(string) bool {
	return false
}
