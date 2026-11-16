package tui

import (
	"golang.org/x/text/unicode/bidi"
)

// ContainsRTLOrArabicNumber reports whether the text holds a strong
// right-to-left character or an Arabic number (Rust
// text_formatting::contains_rtl_or_arabic_number, #61731). Per-grapheme color
// updates redraw individual terminal cells, which breaks native bidi
// rendering, so these headers stay one static span.
func ContainsRTLOrArabicNumber(text string) bool {
	for _, r := range text {
		properties, _ := bidi.LookupRune(r)
		switch properties.Class() {
		case bidi.R, bidi.AL, bidi.AN:
			return true
		}
	}
	return false
}