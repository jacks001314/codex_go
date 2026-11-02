//go:build linux

package tea

import (
	"errors"

	codextui "codex_go/tui"
)

// imagePasteFallsBackToText mirrors Rust's app/clipboard.rs dispatch: the TUI
// pastes clipboard text only when the clipboard reports "no image"
// (PasteImageError::NoImage). A missing or broken clipboard backend surfaces an
// error instead of silently pasting something else.
func imagePasteFallsBackToText(err error) bool {
	var pasteErr codextui.PasteImageError
	if errors.As(err, &pasteErr) {
		return pasteErr.Kind == codextui.PasteImageNoImage
	}
	return false
}
