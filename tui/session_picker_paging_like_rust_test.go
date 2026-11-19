package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"codex_go/session"
)

// Rust's thread/list applies source and eligibility filters in the query, so
// ineligible rows never consume a page slot. The Go store projection filters
// the picker's rows explicitly, and that filtering must happen before the page
// cursor is applied.
func TestSessionSummaryPageFiltersBeforeCursorLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	cwd := "/repo"
	records := []session.Record{
		{
			ID: "exec-new", Title: "Exec", RecencyAt: now,
			Metadata: session.Metadata{CWD: cwd, Source: "exec"},
		},
		{
			ID: "app-new", Title: "App", RecencyAt: now.Add(-time.Minute),
			Metadata: session.Metadata{CWD: cwd, Source: "appServer"},
		},
		{
			ID: "cli-old", Title: "CLI", RecencyAt: now.Add(-time.Hour),
			Metadata: session.Metadata{CWD: cwd, Source: "cli"},
		},
	}
	options := SessionSourceOptions{IncludeArchived: true, CWD: cwd, Limit: 1}
	items, next, err := SessionSummaryPageFromRecords(store, records, options, false)
	if err != nil {
		t.Fatalf("SessionSummaryPageFromRecords() error = %v", err)
	}
	if len(items) != 1 || items[0].ThreadID != "cli-old" {
		t.Fatalf("page = %#v, want the only resumable row", items)
	}
	if next != "" {
		t.Fatalf("next cursor = %q, want the exhausted listing", next)
	}

	options.IncludeNonInteractive = true
	items, next, err = SessionSummaryPageFromRecords(store, records, options, false)
	if err != nil {
		t.Fatalf("SessionSummaryPageFromRecords(non-interactive) error = %v", err)
	}
	if len(items) != 1 || items[0].ThreadID != "exec-new" || next == "" {
		t.Fatalf("non-interactive page = %#v next = %q", items, next)
	}
}

// Rust keeps scroll_top and advances it only when the selection would leave
// the viewport (resume_picker.rs ensure_selected_visible).
func TestSessionPickerScrollsToSelectionLikeRust(t *testing.T) {
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	items := make([]SessionSummary, 0, 40)
	for i := 0; i < 40; i++ {
		items = append(items, SessionSummary{
			ThreadID:  fmt.Sprintf("thread-%02d", i),
			Title:     fmt.Sprintf("Session %02d", i),
			CWD:       "/repo",
			UpdatedAt: now.Add(-time.Duration(i) * time.Minute),
		})
	}
	picker := NewSessionPickerState(SessionPickerResume, items, "")
	picker.Selected = 30
	rows := picker.RenderWindow(80, now, 10)
	if len(rows) != 10 {
		t.Fatalf("window rows = %d, want 10", len(rows))
	}
	// Comfortable rows occupy two lines plus a separator, and the window
	// reserves one row for each visible "more" indicator
	// (resume_picker.rs available_content_rows).
	if picker.ScrollTop != 28 {
		t.Fatalf("scroll top = %d, want the window to end on the selection", picker.ScrollTop)
	}
	if joined := strings.Join(rows, "\n"); !strings.Contains(joined, "Session 30") {
		t.Fatalf("window does not paint the selection:\n%s", joined)
	}

	picker.Selected = 20
	picker.RenderWindow(80, now, 10)
	if picker.ScrollTop != 20 {
		t.Fatalf("scroll top = %d, want it to follow the selection upward", picker.ScrollTop)
	}
	picker.Selected = 23
	picker.RenderWindow(80, now, 10)
	if picker.ScrollTop != 21 {
		t.Fatalf("scroll top = %d, want the window to stay put while the selection is visible", picker.ScrollTop)
	}
}

// Rust's move_down stops at the last loaded row (so the picker can page in
// older sessions) instead of wrapping to the top.
func TestSessionPickerSelectionStopsAtEndsLikeRust(t *testing.T) {
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	items := []SessionSummary{
		{ThreadID: "a", CWD: "/repo", UpdatedAt: now},
		{ThreadID: "b", CWD: "/repo", UpdatedAt: now.Add(-time.Minute)},
		{ThreadID: "c", CWD: "/repo", UpdatedAt: now.Add(-2 * time.Minute)},
	}
	picker := NewSessionPickerState(SessionPickerResume, items, "")
	picker.Move(10)
	if picker.Selected != 2 {
		t.Fatalf("selected = %d, want the last row", picker.Selected)
	}
	picker.Move(-10)
	if picker.Selected != 0 {
		t.Fatalf("selected = %d, want the first row", picker.Selected)
	}
}

// Rust's render_empty_state_line separates first-load, older-page, search and
// exhausted states (resume_picker.rs).
func TestSessionPickerEmptyStatesMatchRust(t *testing.T) {
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	first := NewSessionPickerState(SessionPickerResume, nil, "/repo")
	first.Loading = true
	if got := first.RenderRows(80, now); len(got) != 1 || got[0] != "Loading sessions..." {
		t.Fatalf("first-load rows = %#v", got)
	}

	older := NewSessionPickerState(SessionPickerResume, []SessionSummary{
		{ThreadID: "other", Title: "other workspace", CWD: "/elsewhere"},
	}, "/repo")
	older.Loading = true
	older.NextCursor = "cursor-2"
	if got := older.RenderRows(80, now); len(got) != 1 || got[0] != "Loading older sessions..." {
		t.Fatalf("older-page rows = %#v", got)
	}

	search := NewSessionPickerState(SessionPickerResume, []SessionSummary{
		{ThreadID: "thread-a", Title: "alpha", CWD: "/repo", UpdatedAt: now},
	}, "/repo")
	search.Query = "zzz"
	if got := search.RenderRows(80, now); len(got) != 1 || got[0] != "No results for your search" {
		t.Fatalf("search rows = %#v", got)
	}
	if !search.SearchNeedsMorePages() {
		search.NextCursor = "cursor-2"
		if !search.SearchNeedsMorePages() {
			t.Fatal("a typed query with no match should keep paging")
		}
	}
	search.Loading = true
	if got := search.RenderRows(80, now); len(got) != 1 || got[0] != "Searching..." {
		t.Fatalf("searching rows = %#v", got)
	}
	search.Loading = false
	search.NextCursor = ""
	if search.SearchNeedsMorePages() {
		t.Fatal("an exhausted listing should stop the search")
	}
}

// Rust's render_list keeps the bottom row for a "↓ more" marker while another
// page exists, switches it to "↓ loading more" in flight, and reserves the top
// row for "↑ more" once the window scrolls.
func TestSessionPickerMorePagesHintLikeRust(t *testing.T) {
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	picker := NewSessionPickerState(SessionPickerResume, []SessionSummary{
		{ThreadID: "a", Title: "session-a", CWD: "/repo", UpdatedAt: now},
	}, "/repo")
	if picker.HasMoreBelow(8) {
		t.Fatal("an exhausted listing fits the viewport and has nothing below")
	}
	picker.SetNextPageCursor("cursor-2")
	if !picker.HasMoreBelow(8) {
		t.Fatal("a listing with a cursor always has more below")
	}
	rows := picker.RenderWindow(80, now, 8)
	if got := rows[len(rows)-1]; got != "\u2193 more" {
		t.Fatalf("bottom row = %q, want the more-pages marker", got)
	}
	picker.BeginPageLoad()
	rows = picker.RenderWindow(80, now, 8)
	if got := rows[len(rows)-1]; got != "\u2193 loading more" {
		t.Fatalf("bottom row while loading = %q", got)
	}

	// Overflow without a cursor still paints the marker, and scrolling into
	// the list reserves a row for the upward marker.
	many := make([]SessionSummary, 0, 20)
	for i := 0; i < 20; i++ {
		many = append(many, SessionSummary{
			ThreadID:  fmt.Sprintf("thread-%02d", i),
			Title:     fmt.Sprintf("session-%02d", i),
			CWD:       "/repo",
			UpdatedAt: now.Add(-time.Duration(i) * time.Minute),
		})
	}
	scrolled := NewSessionPickerState(SessionPickerResume, many, "")
	scrolled.Selected = 10
	scrolled.SetNextPageCursor("cursor-2")
	rows = scrolled.RenderWindow(80, now, 8)
	if len(rows) != 8 {
		t.Fatalf("window rows = %d, want 8", len(rows))
	}
	if rows[0] != "\u2191 more" {
		t.Fatalf("top row = %q, want the more-above marker", rows[0])
	}
	if rows[len(rows)-1] != "\u2193 more" {
		t.Fatalf("bottom row = %q, want the more-below marker", rows[len(rows)-1])
	}

	// Overflow alone, with no cursor left, still reports rows below the
	// window (Rust has_more_below's second arm).
	overflow := NewSessionPickerState(SessionPickerResume, many, "")
	if !overflow.HasMoreBelow(8) {
		t.Fatal("a long listing should report rows below the viewport")
	}
}
