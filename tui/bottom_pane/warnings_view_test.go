package bottompane

import (
	"reflect"
	"strings"
	"testing"

	"codex_go/tui/history_cell"
)

func warningsEntriesForTest() []historycell.WarningEntry {
	return []historycell.WarningEntry{
		{
			ID:      historycell.WarningId{Kind: historycell.WarningIdMCPServer, Value: "example"},
			Source:  "MCP \u00b7 example",
			Details: "MCP example could not connect\nSign in again using codex mcp login example",
		},
		{
			ID:      historycell.WarningId{Kind: historycell.WarningIdMessage, Value: "config"},
			Source:  "Startup",
			Details: "Unknown setting `old_option`\nRemove it from config.toml",
		},
		{
			ID:      historycell.WarningId{Kind: historycell.WarningIdMessage, Value: "later"},
			Source:  "Startup",
			Details: "Not yet viewed",
		},
	}
}

// Mirrors Rust's navigation: each list move shows another complete diagnostic
// and reports it as the current page.
func TestWarningsViewNavigationLikeRust(t *testing.T) {
	view := NewWarningsView(warningsEntriesForTest())
	if view.CurrentIndex() != 0 {
		t.Fatalf("initial index = %d, want 0", view.CurrentIndex())
	}
	if entry, ok := view.CurrentEntry(); !ok || entry.ID.Value != "example" {
		t.Fatalf("initial entry = %#v ok=%v", entry, ok)
	}
	view.MoveRight()
	if view.CurrentIndex() != 1 {
		t.Fatalf("after move right index = %d, want 1", view.CurrentIndex())
	}
	if entry, _ := view.CurrentEntry(); entry.ID.Value != "config" {
		t.Fatalf("entry after move right = %#v", entry)
	}
	view.MoveRight()
	view.MoveRight()
	if view.CurrentIndex() != 2 {
		t.Fatalf("move right past the end = %d, want the last page", view.CurrentIndex())
	}
	view.MoveLeft()
	if view.CurrentIndex() != 1 {
		t.Fatalf("after move left index = %d, want 1", view.CurrentIndex())
	}
}

// Mirrors the bounded scrolling of a long diagnostic: the page metrics clamp
// every offset and a page change resets the scroll.
func TestWarningsViewScrollsLongDiagnosticsLikeRust(t *testing.T) {
	view := NewWarningsView(warningsEntriesForTest())
	view.SetPageMetrics(7, 40)
	if view.MaxOffset() != 33 {
		t.Fatalf("MaxOffset = %d, want 33", view.MaxOffset())
	}
	view.JumpBottom()
	if view.Offset() != 33 {
		t.Fatalf("JumpBottom offset = %d, want 33", view.Offset())
	}
	view.JumpTop()
	if view.Offset() != 0 {
		t.Fatalf("JumpTop offset = %d, want 0", view.Offset())
	}
	view.PageDown()
	if view.Offset() != 7 {
		t.Fatalf("PageDown offset = %d, want 7", view.Offset())
	}
	view.PageUp()
	if view.Offset() != 0 {
		t.Fatalf("PageUp offset = %d, want 0", view.Offset())
	}
	view.PageDown()
	view.MoveRight()
	if view.Offset() != 0 {
		t.Fatalf("page change kept offset %d, want 0", view.Offset())
	}
}

// Mirrors `warnings_dismiss_only_drawn_pages_after_navigation_skips_a_frame`: a
// page navigated through without being drawn is not dismissed on close.
func TestWarningsViewDismissesOnlyDrawnPagesLikeRust(t *testing.T) {
	entries := warningsEntriesForTest()
	view := NewWarningsView(entries)
	view.MarkVisited() // page 1 drawn
	view.MoveRight()   // page 2, never drawn
	view.MoveRight()   // page 3
	view.MarkVisited() // page 3 drawn
	view.MoveLeft()    // back to page 2
	dismissed, kept := view.Close()
	want := []historycell.WarningEntry{entries[0], entries[2]}
	if !reflect.DeepEqual(dismissed, want) {
		t.Fatalf("dismissed = %#v, want %#v", dismissed, want)
	}
	if len(kept) != 0 {
		t.Fatalf("kept = %#v, want none", kept)
	}
}

// Mirrors `warnings_keep_current_and_next_without_dismissing_unvisited_pages`:
// keeping a page advances, closing keeps it, and an unvisited page is neither
// dismissed nor kept.
func TestWarningsViewKeepAndNextLikeRust(t *testing.T) {
	entries := warningsEntriesForTest()
	view := NewWarningsView(entries)
	view.MarkVisited()
	if view.KeepAndNext() {
		t.Fatal("keeping a non-final page closed the viewer")
	}
	if view.CurrentIndex() != 1 {
		t.Fatalf("after keep index = %d, want 1", view.CurrentIndex())
	}
	dismissed, kept := view.Close()
	if !reflect.DeepEqual(kept, []historycell.WarningEntry{entries[0]}) {
		t.Fatalf("kept = %#v, want the first entry", kept)
	}
	if len(dismissed) != 0 {
		t.Fatalf("dismissed = %#v, want none", dismissed)
	}

	// Keeping the last page asks the viewer to close.
	single := NewWarningsView(entries[:1])
	if !single.KeepAndNext() {
		t.Fatal("keeping the final page did not close the viewer")
	}
	singleDismissed, singleKept := single.Close()
	if !reflect.DeepEqual(singleKept, []historycell.WarningEntry{entries[0]}) || len(singleDismissed) != 0 {
		t.Fatalf("single keep = (%#v, %#v)", singleDismissed, singleKept)
	}
}

// Mirrors `warnings_view_render.rs`: the title names the current diagnostic, the
// body shows its wrapped details, and the footer carries the hint items.
func TestWarningsViewRenderLikeRust(t *testing.T) {
	view := NewWarningsView(warningsEntriesForTest())
	hints := WarningsHints{Keep: "k", Cancel: "ctrl+c", Copy: "ctrl+o", Navigation: "\u2190/\u2192", Scroll: "\u2193"}
	rows := view.RenderLines(120, 12, hints)
	if len(rows) != 12 {
		t.Fatalf("rendered %d rows, want 12", len(rows))
	}
	if !strings.Contains(rows[0], "Warnings \u00b7 1 of 3 \u00b7 MCP \u00b7 example") {
		t.Fatalf("title = %q", rows[0])
	}
	if rows[1] != "" {
		t.Fatalf("separator = %q, want blank", rows[1])
	}
	body := strings.Join(rows, "\n")
	if !strings.Contains(body, "MCP example could not connect") {
		t.Fatalf("body lost the diagnostic detail:\n%s", body)
	}
	footer := rows[len(rows)-1]
	for _, want := range []string{"k keep & next", "ctrl+c dismiss & close", "ctrl+o copy", "warning", "scroll"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("footer %q missing %q", footer, want)
		}
	}

	// A footer that cannot hold every item drops whole trailing items.
	medium := view.RenderLines(80, 12, hints)
	if got := medium[len(medium)-1]; !strings.Contains(got, "k keep & next") || strings.Contains(got, "scroll") {
		t.Fatalf("medium footer = %q, want the leading items without scroll", got)
	}

	// A narrow footer drops whole trailing items rather than truncating them.
	narrow := view.RenderLines(20, 6, hints)
	if got := narrow[len(narrow)-1]; strings.Contains(got, "scroll") || strings.Contains(got, "copy") {
		t.Fatalf("narrow footer kept trailing items: %q", got)
	}
}

// Mirrors `warning_pages_wrap_and_scroll_long_diagnostics`.
func TestWarningsViewRenderScrollsLongDiagnosticsLikeRust(t *testing.T) {
	entries := warningsEntriesForTest()
	entries[0].Details = "Long diagnostic \u65e5\u672c\u8a9e\n" +
		strings.Repeat("Details retained in full, including remediation instructions.\n", 20) +
		"END OF DIAGNOSTIC"
	view := NewWarningsView(entries)
	hints := WarningsHints{Keep: "k", Cancel: "ctrl+c"}
	if got := strings.Join(view.RenderLines(40, 9, hints), "\n"); !strings.Contains(got, "Long diagnostic") {
		t.Fatalf("first page missing the diagnostic:\n%s", got)
	}
	view.PageDown()
	if got := strings.Join(view.RenderLines(40, 9, hints), "\n"); strings.Contains(got, "Long diagnostic") {
		t.Fatalf("second page still shows the first line:\n%s", got)
	}
	view.JumpBottom()
	if got := strings.Join(view.RenderLines(40, 9, hints), "\n"); !strings.Contains(got, "END OF DIAGNOSTIC") {
		t.Fatalf("bottom page missing the final line:\n%s", got)
	}
	view.MoveRight()
	if got := strings.Join(view.RenderLines(40, 9, hints), "\n"); !strings.Contains(got, "Unknown setting") {
		t.Fatalf("second warning missing:\n%s", got)
	}
}
