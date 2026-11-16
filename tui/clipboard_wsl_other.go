//go:build !linux

package tui

// WSLClipboardCopy is unavailable outside Linux; WSL only exists there.
func WSLClipboardCopy() ClipboardCopyFunc {
	return nil
}