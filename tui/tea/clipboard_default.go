package tea

import (
	sysclipboard "github.com/atotto/clipboard"

	codextui "codex_go/tui"
)

// defaultClipboardWrite mirrors Rust's clipboard backend order
// (tui/clipboard_copy.rs): on WSL the Windows clipboard writer runs first and
// the native WSLg clipboard is its fallback (#53453), because WSLg setup can
// block forever. Every other host writes the native clipboard directly. A
// failed write still falls through to the terminal clipboard in the copy path.
func defaultClipboardWrite() func(string) error {
	if wslCopy := codextui.WSLClipboardCopy(); wslCopy != nil {
		return codextui.ComposeWSLClipboardWrite(wslCopy, sysclipboard.WriteAll)
	}
	return sysclipboard.WriteAll
}