package mermaid

import (
	"unicode"

	"github.com/mattn/go-runewidth"
	"github.com/rivo/uniseg"
)

// renderWidth keeps Mermaid width independent of the host locale.
// go-runewidth's package-level helpers consult EastAsianWidth, which is derived
// from locale environment variables at init. Rust's unicode-width has no such
// mode, so an ambiguous character such as U+2014 is always one cell.
var renderWidth = &runewidth.Condition{}

// charWidth mirrors `UnicodeWidthChar::width`: a scalar's display width, or zero
// when it has none. Rust's `unicode-width` reports zero for combining and format
// characters; go-runewidth misses a few of them (for example the variation
// selectors), so they are zeroed here.
func charWidth(r rune) int {
	if unicode.IsControl(r) || unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf) {
		return 0
	}
	return renderWidth.RuneWidth(r)
}

// textWidth mirrors `UnicodeWidthStr::width`.
//
// Rust's `unicode-width` computes a string width that differs from the sum of
// its scalars for emoji presentation sequences; `uniseg` reproduces that for the
// emoji, CJK and ASCII cases the renderer draws. `unicode-width` 0.2 also
// collapses the Arabic lam-alef ligature, which `uniseg` does not, so each
// collapsed pair is subtracted here (see width_arabic.go).
func textWidth(text string) int {
	return uniseg.StringWidth(text) - lamAlefCollapses(text)
}
