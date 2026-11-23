package tea

import (
	"errors"

	codextui "codex_go/tui"
)

// imagePasteFallsBackToText mirrors Rust's app/clipboard.rs dispatch after
// #63022: clipboard text is pasted only when the image is missing or its
// advertised representation could not be read. A missing or broken clipboard
// backend, an encode failure and an io error surface instead of silently
// pasting something else.
func imagePasteFallsBackToText(err error) bool {
	var pasteErr codextui.PasteImageError
	if !errors.As(err, &pasteErr) {
		return false
	}
	return codextui.PasteImageAllowsTextFallback(pasteErr.Kind)
}
