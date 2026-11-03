//go:build !windows && !linux

package tea

import (
	"fmt"
	"runtime"

	codextui "codex_go/tui"
)

// pasteImageFromClipboardPlatform keeps the pre-existing behaviour on hosts
// without a clipboard reader (Rust returns ClipboardUnavailable on Android).
func pasteImageFromClipboardPlatform() (string, error) {
	return "", codextui.PasteImageError{
		Kind:    codextui.PasteImageClipboardUnavailable,
		Message: fmt.Sprintf("image clipboard is unavailable on %s", runtime.GOOS),
	}
}
