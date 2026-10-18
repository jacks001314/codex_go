package session

import (
	"testing"
	"time"
)

// Rust #51230: time-sorted continuation cursors include the thread id so
// records sharing a timestamp are neither skipped nor duplicated.
func TestListTimeCursorBreaksTimestampTiesLikeRust(t *testing.T) {
	createdAt := time.Unix(1_700_000_000, 0).UTC()
	records := []Record{
		{ID: "thread-a", SessionID: "thread-a", CreatedAt: createdAt, UpdatedAt: createdAt, RecencyAt: createdAt},
		{ID: "thread-b", SessionID: "thread-b", CreatedAt: createdAt, UpdatedAt: createdAt, RecencyAt: createdAt},
		{ID: "thread-c", SessionID: "thread-c", CreatedAt: createdAt, UpdatedAt: createdAt, RecencyAt: createdAt},
	}
	seen := map[ThreadID]bool{}
	cursor := ""
	for page := 0; page < 3; page++ {
		listed, err := ListRecords(records, ListOptions{PageSize: 1, SortKey: SortCreatedAt, SortDirection: SortDesc, Cursor: cursor})
		if err != nil {
			t.Fatalf("ListRecords(page %d) error = %v", page, err)
		}
		if len(listed.Records) != 1 {
			t.Fatalf("page %d records = %#v", page, listed.Records)
		}
		id := listed.Records[0].ID
		if seen[id] {
			t.Fatalf("record %q appeared twice", id)
		}
		seen[id] = true
		cursor = listed.NextCursor
	}
	if len(seen) != 3 || cursor != "" {
		t.Fatalf("seen = %#v, cursor = %q", seen, cursor)
	}

	// Legacy timestamp-only cursors keep the historical skip behavior.
	legacy, err := ListRecords(records, ListOptions{PageSize: 1, SortKey: SortCreatedAt, SortDirection: SortDesc, Cursor: listTimeCursor(createdAt)})
	if err != nil {
		t.Fatalf("legacy cursor error = %v", err)
	}
	if len(legacy.Records) != 0 {
		t.Fatalf("legacy cursor records = %#v, want none", legacy.Records)
	}
}
