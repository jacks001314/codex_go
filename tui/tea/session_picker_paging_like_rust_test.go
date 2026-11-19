package tea

import (
	"strings"
	"testing"
	"time"

	codextui "codex_go/tui"
	"codex_go/tui/keymsg"
)

// Rust's picker requests thread/list one cursor page at a time and starts the
// next page once the selection is within LOAD_NEAR_THRESHOLD rows of the
// loaded end (resume_picker.rs maybe_load_more_for_scroll + load_more_if_needed).
func TestSessionPickerPagesThroughOlderSessionsLikeRust(t *testing.T) {
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	pages := map[string]struct {
		items []codextui.SessionSummary
		next  string
	}{
		"": {
			items: []codextui.SessionSummary{
				{ThreadID: "s1", Title: "session-s1", CWD: "/repo", UpdatedAt: now},
				{ThreadID: "s2", Title: "session-s2", CWD: "/repo", UpdatedAt: now.Add(-time.Minute)},
			},
			next: "cursor-2",
		},
		"cursor-2": {
			items: []codextui.SessionSummary{
				{ThreadID: "s3", Title: "session-s3", CWD: "/repo", UpdatedAt: now.Add(-2 * time.Minute)},
				{ThreadID: "s4", Title: "session-s4", CWD: "/repo", UpdatedAt: now.Add(-3 * time.Minute)},
			},
			next: "cursor-3",
		},
		"cursor-3": {
			items: []codextui.SessionSummary{
				{ThreadID: "s5", Title: "session-s5", CWD: "/repo", UpdatedAt: now.Add(-4 * time.Minute)},
				{ThreadID: "s6", Title: "session-s6", CWD: "/repo", UpdatedAt: now.Add(-5 * time.Minute)},
			},
		},
	}
	var cursors []string
	model := NewModel(codextui.NewState(nil), Options{
		Width:             80,
		Height:            24,
		DisablePasteBurst: true,
		SessionPickerCWD:  "/repo",
		OnListSessionPickerItems: func(scope codextui.SessionPickerScope, cursor string) ([]codextui.SessionSummary, string, string, error) {
			cursors = append(cursors, cursor)
			page := pages[cursor]
			return page.items, page.next, "", nil
		},
	})
	typeText(t, model, "/resume")
	_, cmd := model.Update(key(keymsg.KeyEnter))
	model = runTeaCmdTree(t, model, cmd)

	if got, want := strings.Join(cursors, ","), ",cursor-2,cursor-3"; got != want {
		t.Fatalf("requested cursors = %q, want %q", got, want)
	}
	if model.modal == nil || model.modal.sessionPicker == nil {
		t.Fatalf("resume picker not open: %#v", model.modal)
	}
	picker := model.modal.sessionPicker
	if len(picker.Items) != 6 {
		t.Fatalf("loaded rows = %d, want all three pages", len(picker.Items))
	}
	if picker.NextCursor != "" || picker.MorePagesAvailable {
		t.Fatalf("listing should be exhausted, cursor = %q", picker.NextCursor)
	}
	rows := strings.Join(picker.RenderRows(80, now), "\n")
	if !strings.Contains(rows, "session-s6") {
		t.Fatalf("oldest page missing from the picker rows: %q", rows)
	}
}

// Rust paints a trailing "Loading older sessions..." row while the next page
// is in flight (resume_picker.rs render_rows).
func TestSessionPickerShowsLoadingOlderRowsWhilePagingLikeRust(t *testing.T) {
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	picker := codextui.NewSessionPickerState(codextui.SessionPickerResume, []codextui.SessionSummary{
		{ThreadID: "s1", Title: "session-s1", CWD: "/repo", UpdatedAt: now},
	}, "/repo")
	picker.SetNextPageCursor("cursor-2")
	picker.Loading = true
	rows := strings.Join(picker.RenderWindow(80, now, 10), "\n")
	if !strings.Contains(rows, "Loading older sessions...") {
		t.Fatalf("rows = %q, want the loading-older row", rows)
	}
}

// A page that arrives after the user changed the filter belongs to the old
// listing and must be dropped (Rust PickerState::listing_token).
func TestSessionPickerDropsPagesFromAnOlderFilterLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{
		Width:              80,
		Height:             24,
		DisablePasteBurst:  true,
		SessionPickerCWD:   "/repo",
		SessionPickerItems: []codextui.SessionSummary{{ThreadID: "s1", Title: "session-s1", CWD: "/repo"}},
		OnListSessionPickerItems: func(scope codextui.SessionPickerScope, cursor string) ([]codextui.SessionSummary, string, string, error) {
			return nil, "", "", nil
		},
	})
	typeText(t, model, "/resume")
	_, cmd := model.Update(key(keymsg.KeyEnter))
	runTeaCmd(t, model, cmd)
	if model.modal == nil || model.modal.sessionPicker == nil {
		t.Fatalf("resume picker not open: %#v", model.modal)
	}
	staleToken := model.modal.sessionPicker.ListingToken()
	// The All filter restarts the listing (Rust change_filter_mode).
	model.Update(key(keymsg.KeyRight))
	model.Update(SessionPickerRefreshMsg{
		PickerID:     model.modal.id,
		Items:        []codextui.SessionSummary{{ThreadID: "stale", CWD: "/repo"}},
		ListingToken: staleToken,
	})
	for _, item := range model.modal.sessionPicker.Items {
		if item.ThreadID == "stale" {
			t.Fatalf("stale page merged into the new listing: %#v", model.modal.sessionPicker.Items)
		}
	}
}

// Rust keeps requesting older pages while a typed query matches none of the
// loaded rows (resume_picker.rs set_query -> LoadTrigger::Search).
func TestSessionPickerSearchesOlderPagesLikeRust(t *testing.T) {
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	pages := map[string]struct {
		items []codextui.SessionSummary
		next  string
	}{
		"": {
			items: []codextui.SessionSummary{
				{ThreadID: "s1", Title: "recent one", CWD: "/repo", UpdatedAt: now},
				{ThreadID: "s2", Title: "recent two", CWD: "/repo", UpdatedAt: now.Add(-time.Minute)},
			},
			next: "cursor-2",
		},
		"cursor-2": {
			items: []codextui.SessionSummary{
				{ThreadID: "s3", Title: "ancient target", CWD: "/repo", UpdatedAt: now.Add(-48 * time.Hour)},
			},
		},
	}
	model := NewModel(codextui.NewState(nil), Options{
		Width:             80,
		Height:            24,
		DisablePasteBurst: true,
		SessionPickerCWD:  "/repo",
		OnListSessionPickerItems: func(scope codextui.SessionPickerScope, cursor string) ([]codextui.SessionSummary, string, string, error) {
			page := pages[cursor]
			return page.items, page.next, "", nil
		},
	})
	typeText(t, model, "/resume")
	_, cmd := model.Update(key(keymsg.KeyEnter))
	model = runTeaCmdTree(t, model, cmd)
	if model.modal == nil || model.modal.sessionPicker == nil {
		t.Fatalf("resume picker not open: %#v", model.modal)
	}

	_, cmd = model.Update(runes("target"))
	model = runTeaCmdTree(t, model, cmd)

	picker := model.modal.sessionPicker
	if visible := picker.VisibleItems(); len(visible) != 1 || visible[0].ThreadID != "s3" {
		t.Fatalf("search results = %#v, want the page-two row", visible)
	}
}

// Opening the picker must never run thread/list (or the linked-checkout scan)
// on the Update goroutine: Rust paints the first frame and loads the page in
// the background (resume_picker.rs spawn_app_server_source).
func TestSessionPickerOpenDoesNotRunTheListingInlineLikeRust(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	model := NewModel(codextui.NewState(nil), Options{
		Width:             80,
		Height:            24,
		DisablePasteBurst: true,
		SessionPickerCWD:  "/repo",
		OnListSessionPickerItems: func(scope codextui.SessionPickerScope, cursor string) ([]codextui.SessionSummary, string, string, error) {
			close(started)
			<-release
			return []codextui.SessionSummary{{ThreadID: "s1", Title: "loaded later", CWD: "/repo"}}, "", "", nil
		},
	})
	typeText(t, model, "/resume")
	_, cmd := model.Update(key(keymsg.KeyEnter))
	if model.modal == nil || model.modal.sessionPicker == nil {
		t.Fatalf("picker should open before the listing runs: %#v", model.modal)
	}
	select {
	case <-started:
		t.Fatal("listing ran on the Update goroutine")
	default:
	}

	done := make(chan struct{})
	go func() {
		runTeaCmd(t, model, cmd)
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("listing completed before it was released")
	case <-started:
	}
	close(release)
	<-done
	if rows := strings.Join(model.modal.sessionPicker.RenderRows(80, model.currentTime()), "\n"); !strings.Contains(rows, "loaded later") {
		t.Fatalf("background page missing from the picker: %q", rows)
	}
}
