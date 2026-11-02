package tui

import (
	"github.com/mattn/go-runewidth"
)

// Rust parity: the TUI's width model matches `unicode-width`/ratatui, where
// East Asian ambiguous characters such as "\u2190" (left arrow), "\u00b7"
// (middle dot) and "\u26a0" (warning sign) occupy one column.
//
// go-runewidth flips ambiguous characters to two columns when the process
// locale is CJK, which silently shrinks every width budget by one column per
// ambiguous character: wrapping breaks early, truncation eats real text, and
// right-aligned rows (the footer warning notice, for example) land short.
// Terminals render those characters as narrow, so pin the narrow model once
// at startup instead of inheriting the locale.
func init() {
	runewidth.EastAsianWidth = false
	if !runewidth.DefaultCondition.EastAsianWidth {
		return
	}
	runewidth.DefaultCondition.EastAsianWidth = false
	// CreateLUT caches widths per condition; rebuild it so the pinned model
	// applies to every later measurement.
	runewidth.CreateLUT()
}
