package tui

import (
	"testing"

	"github.com/mattn/go-runewidth"
)

// Rust's unicode-width treats East Asian ambiguous characters as one column, and
// terminals render them that way. The package must not inherit a CJK locale's
// two-column model, or every width budget loses a column per "\u2190"/"\u00b7"/"\u26a0".
func TestEastAsianAmbiguousWidthsMatchRust(t *testing.T) {
	for _, sample := range []string{"\u2190", "\u00b7", "\u26a0", "\u2026"} {
		if got := runewidth.StringWidth(sample); got != 1 {
			t.Fatalf("StringWidth(%q) = %d, want 1", sample, got)
		}
	}
	if got := runewidth.StringWidth("\u2190 for agents \u00b7 ? for shortcuts"); got != 30 {
		t.Fatalf("hint width = %d, want 30", got)
	}
}
