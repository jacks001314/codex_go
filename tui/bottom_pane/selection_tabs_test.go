package bottompane

import (
	"strings"
	"testing"

	"codex_go/tui"
)

// Mirrors Rust's filled_tabs_window_around_active_tab snapshot: the bar keeps the
// active tab, grows around it while the cells fit, and shows arrows for the
// hidden neighbours.
func TestFilledTabBarWindowMatchesRust(t *testing.T) {
	labels := []string{"All", "Common", "Customized (3)", "Unbound (4)", "App", "Composer", "Debug"}
	cases := []struct {
		active int
		want   string
	}{
		{0, " All   Common                \u203a"},
		{2, "\u2039  Common   Customized (3)   \u203a"},
		{6, "\u2039  App   Composer   Debug"},
	}
	for _, tc := range cases {
		got := strings.TrimRight(FilledTabBarText(labels, tc.active, 30), " ")
		if got != strings.TrimRight(tc.want, " ") {
			t.Fatalf("active %d bar = %q, want %q", tc.active, got, tc.want)
		}
		var active *FilledTabCell
		for _, cell := range FilledTabBar(labels, tc.active, 30) {
			if cell.Active {
				cell := cell
				active = &cell
			}
		}
		if active == nil {
			t.Fatalf("active %d bar has no active cell", tc.active)
		}
		if strings.TrimSpace(active.Text) != labels[tc.active] {
			t.Fatalf("active %d cell = %q", tc.active, active.Text)
		}
		if tui.DisplayWidth(active.Text) != tui.DisplayWidth(labels[tc.active])+2 {
			t.Fatalf("active %d cell width = %q", tc.active, active.Text)
		}
	}
}

// Mirrors Rust's filled_tabs_truncate_unicode_without_hiding_active_tab: narrow
// bars truncate the active cell with an ellipsis instead of dropping it. Rust's
// snapshot text shows the CJK continuation cells as blanks (its reader maps an
// empty buffer symbol to a space), so the Go strings differ only in those
// artifact columns.
func TestFilledTabBarTruncatesUnicodeMatchesRust(t *testing.T) {
	labels := []string{"All", "\u6587\u4ef6\u7cfb\u7edf plugins", "Debug"}
	cases := []struct {
		width int
		want  string
	}{
		{1, "\u2026"},
		{4, " \u6587\u2026"},
		{9, "\u2039  \u6587\u2026  \u203a"},
		{16, "\u2039  \u6587\u4ef6\u7cfb\u7edf p\u2026 \u203a"},
	}
	for _, tc := range cases {
		got := strings.TrimRight(FilledTabBarText(labels, 1, tc.width), " ")
		if got != strings.TrimRight(tc.want, " ") {
			t.Fatalf("width %d bar = %q, want %q", tc.width, got, tc.want)
		}
		if bar := FilledTabBarText(labels, 1, tc.width); tui.DisplayWidth(strings.TrimRight(bar, " ")) > tc.width {
			t.Fatalf("width %d bar exceeds its width: %q", tc.width, bar)
		}
	}
}

// Rust keeps a marker off a strip that is too narrow for it: the left marker
// needs five columns and the right one seven.
func TestFilledTabBarMarkersNeedRoom(t *testing.T) {
	labels := []string{"All", "Common", "Customized (3)"}
	if bar := FilledTabBarText(labels, 1, 4); strings.Contains(bar, FilledTabMarkerLeft) {
		t.Fatalf("width 4 bar should not show the left marker: %q", bar)
	} else if strings.TrimSpace(tui.TruncateWithEllipsis(" Common ", 4)) == "" {
		t.Fatalf("width 4 bar = %q", bar)
	}
	if bar := FilledTabBarText(labels, 1, 5); !strings.Contains(bar, FilledTabMarkerLeft) {
		t.Fatalf("width 5 bar should show the left marker: %q", bar)
	}
	if bar := FilledTabBarText(labels, 1, 6); strings.Contains(bar, FilledTabMarkerRight) {
		t.Fatalf("width 6 bar should not show the right marker: %q", bar)
	}
	if bar := FilledTabBarText(labels, 1, 7); !strings.Contains(bar, FilledTabMarkerRight) {
		t.Fatalf("width 7 bar should show the right marker: %q", bar)
	}
	if lines := FilledTabBarLines(nil, 0, 10); lines != nil {
		t.Fatalf("empty labels = %#v", lines)
	}
	if lines := FilledTabBarLines(labels, 0, 0); lines != nil {
		t.Fatalf("zero width = %#v", lines)
	}
}
