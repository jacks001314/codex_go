package tui

import (
	"testing"
	"time"
)

func ingestTestItem(backend BackendID, threadID string, updated time.Time) SessionSummary {
	return SessionSummary{BackendID: backend, ThreadID: threadID, UpdatedAt: updated, CreatedAt: updated}
}

// TestSessionPickerIngestPreservesSelectionLikeRust mirrors Rust #57862: when a
// delayed page reorders the merged rows by timestamp, the selected session stays
// selected instead of moving to whatever row now occupies its old index.
func TestSessionPickerIngestPreservesSelectionLikeRust(t *testing.T) {
	backend := NewBackendID()
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	items := []SessionSummary{
		ingestTestItem(backend, "newest", now),
		ingestTestItem(backend, "middle", now.Add(-time.Hour)),
		ingestTestItem(backend, "oldest", now.Add(-2*time.Hour)),
	}
	picker := NewSessionPickerStateForBackend(backend, SessionPickerResume, items, "")
	picker.Select(1)
	if item, ok := picker.SelectedItem(); !ok || item.ThreadID != "middle" {
		t.Fatalf("initial selection = %#v, want middle", item)
	}

	// A delayed, newer session arrives from another backend and must sort to
	// the top without stealing the selection.
	other := NewBackendID()
	picker.IngestItems([]SessionSummary{ingestTestItem(other, "delayed", now.Add(30*time.Minute))})
	if item, ok := picker.SelectedItem(); !ok || item.ThreadID != "middle" {
		t.Fatalf("selection after ingest = %#v, want middle", item)
	}
	visible := picker.VisibleItems()
	if len(visible) != 4 || visible[0].ThreadID != "delayed" {
		t.Fatalf("merged order = %#v", visible)
	}
}

// TestSessionPickerIngestKeepsPendingPageDownDistanceLikeRust keeps a forward
// page-down that ran past the loaded rows until the next page arrives.
func TestSessionPickerIngestKeepsPendingPageDownDistanceLikeRust(t *testing.T) {
	backend := NewBackendID()
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	items := make([]SessionSummary, 0, 5)
	for index := 0; index < 5; index++ {
		items = append(items, ingestTestItem(backend, "row-"+string(rune('a'+index)), now.Add(-time.Duration(index)*time.Hour)))
	}
	picker := NewSessionPickerStateForBackend(backend, SessionPickerResume, items, "")
	picker.MorePagesAvailable = true
	picker.Select(2)
	picker.MovePage(6)
	if picker.hasPendingPageDown != true {
		t.Fatalf("page-down past the end did not pend: selected=%d pending=%v", picker.Selected, picker.hasPendingPageDown)
	}
	if item, ok := picker.SelectedItem(); !ok || item.ThreadID != "row-c" {
		t.Fatalf("pending page-down moved selection = %#v", item)
	}

	// Three more (older) rows arrive; the pending distance of 6 resolves to row-c+6.
	arrived := make([]SessionSummary, 0, 3)
	for index := 5; index < 8; index++ {
		arrived = append(arrived, ingestTestItem(backend, "row-"+string(rune('a'+index)), now.Add(-time.Duration(index)*time.Hour)))
	}
	picker.MorePagesAvailable = false
	picker.IngestItems(arrived)
	if picker.hasPendingPageDown {
		t.Fatal("pending page-down survived the page that satisfied it")
	}
	if picker.Selected != 7 {
		t.Fatalf("resolved page-down selected=%d, want 7", picker.Selected)
	}
	if item, ok := picker.SelectedItem(); !ok || item.ThreadID != "row-h" {
		t.Fatalf("resolved selection = %#v", item)
	}
}
