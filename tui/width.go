package tui

import (
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
	"github.com/rivo/uniseg"
)

// Rust parity: codex-rs/tui/src/width.rs.

// PhysicalRowCount returns the terminal rows a plain-text line occupies at
// wrapWidth. A wide cluster that would start in the last column moves to the
// next row, so the unused column counts toward the rows (Rust #63151
// `physical_row_count`); clusters that start with a narrow character may
// straddle the edge. Dividing the line's display width by wrapWidth misses the
// gap and can undercount long URLs with wide characters.
func PhysicalRowCount(text string, wrapWidth int) int {
	if wrapWidth <= 0 {
		return 1
	}
	cells := 0
	graphemes := uniseg.NewGraphemes(text)
	for graphemes.Next() {
		cluster := graphemes.Str()
		width := displayWidthOfGrapheme(cluster)
		if width > 1 && wrapWidth > 1 && cells%wrapWidth == wrapWidth-1 {
			first, _ := utf8.DecodeRuneInString(cluster)
			if first != utf8.RuneError && runewidth.StringWidth(string(first)) == 2 {
				cells++
			}
		}
		cells += width
	}
	if cells < 1 {
		cells = 1
	}
	return (cells + wrapWidth - 1) / wrapWidth
}

// UsableContentWidth returns the positive content width left after reserving
// fixed columns for prefixes, gutters, bullets, or labels.
func UsableContentWidth(totalWidth int, reservedCols int) (int, bool) {
	if totalWidth <= reservedCols {
		return 0, false
	}
	return totalWidth - reservedCols, true
}
