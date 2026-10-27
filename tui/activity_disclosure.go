package tui

// ActivityDisclosure describes the inline detail control an owned transcript
// cell exposes. Rust parity: codex-rs/tui/src/history_cell/activity_preview.rs.
type ActivityDisclosure struct {
	// HiddenLines is the number of retained output lines hidden by a compact
	// preview. Zero means the cell cannot report a line count.
	HiddenLines int
	// Generic marks details that can be revealed without a retained-output
	// line count (for example an exploring or computer-activity group).
	Generic bool
}

// Visible reports whether the cell offers an inline disclosure control.
func (d ActivityDisclosure) Visible() bool {
	return d.HiddenLines > 0 || d.Generic
}
