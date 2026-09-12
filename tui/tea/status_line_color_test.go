package tea

import (
	"testing"

	bottompane "codex_go/tui/bottom_pane"
)

func TestSoftenStatusLineColorSGRMatchesRust(t *testing.T) {
	// Rust's status_line_style_tests: #ff0000 softens to rgb(228, 11, 11).
	if got := softenStatusLineColorSGR("#ff0000"); got != "\x1b[38;2;228;11;11m" {
		t.Fatalf("softened red = %q", got)
	}
	if got := softenStatusLineColorSGR("not-a-color"); got != "" {
		t.Fatalf("invalid color = %q, want empty", got)
	}
}

func TestStatusLineAccentSGRBorrowsThemeColors(t *testing.T) {
	// A real theme must yield a truecolor foreground for at least one accent.
	if got := statusLineAccentSGR("catppuccin-mocha", bottompane.StatusLineAccentModel); got == "" {
		t.Fatal("model accent had no theme color for catppuccin-mocha")
	}
	// An accent with no mapped tokens resolves to the palette fallback (empty).
	if got := statusLineAccentSGR("catppuccin-mocha", bottompane.StatusLineAccentNone); got != "" {
		t.Fatalf("none accent = %q, want empty", got)
	}
}
