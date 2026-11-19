package tui

import (
	"fmt"
	"testing"
	"time"
)

// Rust's comfortable list paints a blank separator line between sessions while
// the dense list does not (resume_picker.rs row_separator_height).
func TestSessionPickerComfortableSeparatorsLikeRust(t *testing.T) {
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	items := []SessionSummary{
		{ThreadID: "a", Title: "session-a", CWD: "/repo", UpdatedAt: now},
		{ThreadID: "b", Title: "session-b", CWD: "/repo", UpdatedAt: now.Add(-time.Minute)},
		{ThreadID: "c", Title: "session-c", CWD: "/repo", UpdatedAt: now.Add(-2 * time.Minute)},
	}
	comfortable := NewSessionPickerState(SessionPickerResume, items, "")
	rows := comfortable.RenderRows(80, now)
	if len(rows) < 5 {
		t.Fatalf("comfortable rows = %#v", rows)
	}
	separators := 0
	for _, row := range rows {
		if row == "" {
			separators++
		}
	}
	if separators != len(items)-1 {
		t.Fatalf("separator rows = %d in %#v, want %d", separators, rows, len(items)-1)
	}

	dense := NewSessionPickerState(SessionPickerResume, items, "")
	dense.ToggleDensity()
	if got := dense.RenderRows(80, now); len(got) != len(items) {
		t.Fatalf("dense rows = %d, want one line per session (%d)", len(got), len(items))
	}
}

// Rust reports the footer percent from the scroll position, not the selection,
// and freezes it while a page is in flight (resume_picker.rs
// picker_footer_scroll_percent + frozen_footer_percent).
func TestSessionPickerFooterPercentScrollsAndFreezesLikeRust(t *testing.T) {
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	items := make([]SessionSummary, 0, 10)
	for i := 0; i < 10; i++ {
		items = append(items, SessionSummary{
			ThreadID:  fmt.Sprintf("thread-%d", i),
			Title:     fmt.Sprintf("row %d", i),
			CWD:       "/repo",
			UpdatedAt: now.Add(-time.Duration(i) * time.Minute),
		})
	}
	picker := NewSessionPickerState(SessionPickerResume, items, "")
	picker.RenderWindow(80, now, 6)
	if got := picker.FooterPercent(); got != 0 {
		t.Fatalf("percent at the top = %d, want 0", got)
	}

	picker.Selected = len(items) - 1
	picker.RenderWindow(80, now, 6)
	if got := picker.FooterPercent(); got != 100 {
		t.Fatalf("percent at the end = %d, want 100", got)
	}

	picker.Selected = 5
	picker.RenderWindow(80, now, 6)
	before := picker.FooterPercent()
	if before <= 0 || before >= 100 {
		t.Fatalf("percent mid-list = %d, want a partial position", before)
	}
	picker.BeginPageLoad()
	picker.ScrollTop = 0
	if got := picker.FooterPercent(); got != before {
		t.Fatalf("percent while loading = %d, want the frozen %d", got, before)
	}
	picker.Loading = false
	if got := picker.FooterPercent(); got == before {
		t.Fatalf("percent after loading = %d, want the live position", got)
	}
}

// Rust fits " position / total · percent " into the separator, marks an
// in-flight total with an ellipsis, and falls back to the bare percent on
// narrow views (resume_picker.rs picker_footer_progress_label).
func TestSessionPickerFooterProgressLabelLikeRust(t *testing.T) {
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	items := make([]SessionSummary, 0, 10)
	for i := 0; i < 10; i++ {
		items = append(items, SessionSummary{
			ThreadID:  fmt.Sprintf("thread-%d", i),
			Title:     fmt.Sprintf("row %d", i),
			CWD:       "/repo",
			UpdatedAt: now.Add(-time.Duration(i) * time.Minute),
		})
	}
	picker := NewSessionPickerState(SessionPickerResume, items, "")
	// Rust's label test keeps every row one line tall.
	picker.ToggleDensity()
	picker.Selected = 2
	picker.RenderWindow(80, now, 6)
	if got, want := picker.footerProgressLabel(80), " 3 / 10 \u00b7 0% "; got != want {
		t.Fatalf("label = %q, want %q", got, want)
	}
	picker.SetNextPageCursor("cursor-1")
	picker.BeginPageLoad()
	if got, want := picker.footerProgressLabel(80), " 3 / 10\u2026 \u00b7 0% "; got != want {
		t.Fatalf("loading label = %q, want %q", got, want)
	}
	if got, want := picker.footerProgressLabel(10), " 0% "; got != want {
		t.Fatalf("narrow label = %q, want %q", got, want)
	}
}

// Rust loads another page when the first one does not fill the viewport
// (resume_picker.rs ensure_minimum_rows_for_view).
func TestSessionPickerLoadsMoreWhenTheViewportIsNotFilledLikeRust(t *testing.T) {
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	picker := NewSessionPickerState(SessionPickerResume, []SessionSummary{
		{ThreadID: "a", Title: "session-a", CWD: "/repo", UpdatedAt: now},
		{ThreadID: "b", Title: "session-b", CWD: "/repo", UpdatedAt: now.Add(-time.Minute)},
	}, "")
	picker.RenderWindow(80, now, 12)
	if picker.NeedsMinimumRowsForView(12) {
		t.Fatal("an exhausted listing should not chase more pages")
	}
	picker.SetNextPageCursor("cursor-2")
	if !picker.NeedsMinimumRowsForView(12) {
		t.Fatal("a page that leaves the viewport half empty should request more")
	}
	if picker.NeedsMinimumRowsForView(3) {
		t.Fatal("a viewport the page already fills should not request more")
	}
	picker.Loading = true
	if picker.NeedsMinimumRowsForView(12) {
		t.Fatal("an in-flight page should not stack another request")
	}
}
