package chatwidget

import (
	"strings"
	"testing"
)

// TestStaticPagerChromeLikeRust pins the /diff pager chrome to Rust
// pager_overlay::StaticOverlay: "/ TITLE" over a "/ " fill, "~" filler rows, a
// rule carrying the percentage, the navigation hint, the close-only action hint
// and one blank row.
func TestStaticPagerChromeLikeRust(t *testing.T) {
	overlay := NewStaticPagerOverlay(120, 40, "first line\nsecond line", "D I F F")
	rows := strings.Split(overlay.View(), "\n")
	if len(rows) != 40 {
		t.Fatalf("rows = %d, want 40\n%s", len(rows), overlay.View())
	}
	if !strings.HasPrefix(rows[0], "/ D I F F / / ") {
		t.Fatalf("header = %q", rows[0])
	}
	// "/ " repeats to the full width so the row ends on a space; the screen
	// capture trims it, leaving 119 visible columns like the Rust snapshot.
	if got := len(strings.TrimRight(rows[0], " ")); got != 119 {
		t.Fatalf("header width = %d, want 119", got)
	}
	if rows[1] != "first line" || rows[2] != "second line" {
		t.Fatalf("content rows = %q %q", rows[1], rows[2])
	}
	if rows[3] != "~"+strings.Repeat(" ", 119) {
		t.Fatalf("filler row = %q", rows[3])
	}
	if rows[36] != staticPagerSeparator(120, 100) {
		t.Fatalf("separator = %q", rows[36])
	}
	if !strings.HasSuffix(rows[36], " 100% \u2500") {
		t.Fatalf("separator percent = %q", rows[36])
	}
	if rows[37] != "↑/↓ to scroll · pgup/pgdn to page · home/end to jump" {
		t.Fatalf("navigation hint = %q", rows[37])
	}
	if rows[38] != "q close" {
		t.Fatalf("action hint = %q", rows[38])
	}
	if rows[39] != "" {
		t.Fatalf("trailing row = %q", rows[39])
	}
}

// TestStaticPagerStartsAtTopAndReportsScrollLikeRust pins that `/diff` opens at
// the top of the content (Rust PagerView::new seeds scroll_offset 0) and that
// the percentage follows the scroll offset.
func TestStaticPagerStartsAtTopAndReportsScrollLikeRust(t *testing.T) {
	lines := make([]string, 100)
	for index := range lines {
		lines[index] = "line"
	}
	overlay := NewStaticPagerOverlay(120, 40, strings.Join(lines, "\n"), "D I F F")
	if overlay.YOffset() != 0 {
		t.Fatalf("initial offset = %d, want 0", overlay.YOffset())
	}
	if got := overlay.scrollPercent(); got != 0 {
		t.Fatalf("initial percent = %d, want 0", got)
	}
	for range 10 {
		overlay.ApplyPagerAction(PagerScrollDown)
	}
	if got := overlay.scrollPercent(); got <= 0 || got >= 100 {
		t.Fatalf("percent after scrolling = %d", got)
	}
	overlay.ApplyPagerAction(PagerJumpBottom)
	if got := overlay.scrollPercent(); got != 100 {
		t.Fatalf("percent at bottom = %d, want 100", got)
	}
}
