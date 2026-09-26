package mermaid

import (
	"unicode"

	"github.com/mattn/go-runewidth"
	"github.com/rivo/uniseg"
)

// charWidth mirrors `UnicodeWidthChar::width`: a scalar's display width, or zero
// when it has none. Rust's `unicode-width` reports zero for combining and format
// characters; go-runewidth misses a few of them (for example the variation
// selectors), so they are zeroed here.
func charWidth(r rune) int {
	if unicode.IsControl(r) || unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf) {
		return 0
	}
	return runewidth.RuneWidth(r)
}

// textWidth mirrors `UnicodeWidthStr::width`.
//
// Rust's `unicode-width` computes a string width that differs from the sum of
// its scalars for emoji presentation sequences; `uniseg` reproduces that for the
// emoji, CJK and ASCII cases the renderer draws. Its one known divergence is
// Arabic: `unicode-width` 0.2 collapses a lam-alef ligature, so Rust rejects
// such labels through `check_label_text`'s ligature check while Go accepts them.
func textWidth(text string) int { return uniseg.StringWidth(text) }
