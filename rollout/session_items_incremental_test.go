package rollout

import (
	"testing"
	"time"
)

func TestRecordFromPathRefreshesRepeatedItemSnapshotInCreationOrder(t *testing.T) {
	now := time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC)
	recorder, err := NewRecorder(&CreateParams{
		CodexHome: t.TempDir(),
		SessionID: "session-updated-item",
		ThreadID:  "thread-updated-item",
		CWD:       "D:/repo",
		Now:       now,
	})
	if err != nil {
		t.Fatal(err)
	}
	first := Item{
		ID:       "item-1",
		Type:     "message",
		Role:     "assistant",
		Text:     "partial",
		Metadata: map[string]any{"turnId": "turn-1"},
	}
	if err := recorder.AppendItem(first); err != nil {
		t.Fatal(err)
	}
	updated := first
	updated.Text = "final"
	if err := recorder.AppendItem(updated); err != nil {
		t.Fatal(err)
	}
	path := recorder.Path()
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}

	record, err := RecordFromPath(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(record.Items) != 1 {
		t.Fatalf("items = %#v, want one refreshed snapshot", record.Items)
	}
	item := record.Items[0]
	if item.Text != "final" {
		t.Fatalf("item text = %q, want final", item.Text)
	}
	if item.CreatedAtOrdinal != 2 || item.UpdatedAtOrdinal != 3 {
		t.Fatalf("item ordinals = created %d updated %d", item.CreatedAtOrdinal, item.UpdatedAtOrdinal)
	}
}
