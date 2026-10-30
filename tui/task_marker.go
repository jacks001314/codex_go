package tui

// Task marker glyphs mirror Rust style::task_marker (#58264): a fixed
// ChatGPT-blue dot for unacknowledged results and a grey hollow dot otherwise.
const (
	taskMarkerUnreadGlyph = "\u25cf"
	taskMarkerReadGlyph   = "\u25cb"
	taskMarkerUnreadSGR   = "\x1b[38;2;59;130;246m"
	taskMarkerReadSGR     = "\x1b[38;2;128;128;128m"
	taskMarkerResetSGR    = "\x1b[39m"
)

// StyledTaskMarker returns the styled marker span. Pickers splice it in after
// the row style so the fixed truecolor survives selection backgrounds, exactly
// like Rust task_marker(unread, row_style).
func StyledTaskMarker(unread bool) string {
	if unread {
		return taskMarkerUnreadSGR + taskMarkerUnreadGlyph + taskMarkerResetSGR
	}
	return taskMarkerReadSGR + taskMarkerReadGlyph + taskMarkerResetSGR
}
