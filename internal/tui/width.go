package tui

// Rust parity: codex-rs/tui/src/width.rs.

// UsableContentWidth returns the positive content width left after reserving
// fixed columns for prefixes, gutters, bullets, or labels.
func UsableContentWidth(totalWidth int, reservedCols int) (int, bool) {
	if totalWidth <= reservedCols {
		return 0, false
	}
	return totalWidth - reservedCols, true
}
