package session

import (
	"errors"
	"testing"
	"time"
)

func TestLiveThreadOwnsWriterAndStoreOperationsUntilClose(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	record := &Record{
		ID:        "thread-live",
		CreatedAt: time.Now().UTC(),
		Metadata:  Metadata{HistoryMode: "paginated"},
	}
	if err := store.Create(record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	guard, err := OpenLiveThread(store, record, true)
	if err != nil {
		t.Fatalf("OpenLiveThread() error = %v", err)
	}
	defer func() { _ = guard.Discard() }()
	liveThread := guard.Commit()
	defer func() { _ = liveThread.Close() }()

	if liveThread.ThreadID() != record.ID || liveThread.HistoryMode() != "paginated" {
		t.Fatalf("live thread identity = %q/%q", liveThread.ThreadID(), liveThread.HistoryMode())
	}
	if !liveThread.OwnsWriter() {
		t.Fatal("paginated live thread does not own its writer")
	}
	if _, err := NewStore(root).AcquireWriter(record.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("competing AcquireWriter() error = %v, want ErrConflict", err)
	}
	appended, err := liveThread.AppendItems([]Item{{ID: "item-1", Text: "hello"}})
	if err != nil || len(appended.Items) != 1 {
		t.Fatalf("AppendItems() = %#v, %v", appended, err)
	}
	loaded, err := liveThread.Read(true, true)
	if err != nil || len(loaded.Items) != 1 {
		t.Fatalf("Read() = %#v, %v", loaded, err)
	}
	loaded.Title = "updated"
	if err := liveThread.Save(loaded); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	metadataTitle := "metadata-updated"
	updated, err := liveThread.UpdateMetadata(&MetadataPatch{Title: &metadataTitle}, true)
	if err != nil || updated.Title != metadataTitle {
		t.Fatalf("UpdateMetadata() = %#v, %v", updated, err)
	}

	if err := liveThread.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := liveThread.Read(true, true); !errors.Is(err, ErrConflict) {
		t.Fatalf("Read() after Close error = %v, want ErrConflict", err)
	}
	if _, err := liveThread.UpdateMetadata(&MetadataPatch{}, true); !errors.Is(err, ErrConflict) {
		t.Fatalf("UpdateMetadata() after Close error = %v, want ErrConflict", err)
	}
	transferred, err := NewStore(root).AcquireWriter(record.ID)
	if err != nil {
		t.Fatalf("AcquireWriter() after Close error = %v", err)
	}
	if err := transferred.Close(); err != nil {
		t.Fatalf("transferred.Close() error = %v", err)
	}
}

func TestLiveThreadInitGuardDiscardReleasesWriter(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	record := &Record{ID: "thread-discard", Metadata: Metadata{HistoryMode: "paginated"}}
	guard, err := OpenLiveThread(store, record, true)
	if err != nil {
		t.Fatalf("OpenLiveThread() error = %v", err)
	}
	if guard.LiveThread() == nil {
		t.Fatal("guard did not retain live thread")
	}
	if err := guard.Discard(); err != nil {
		t.Fatalf("Discard() error = %v", err)
	}
	if err := guard.Discard(); err != nil {
		t.Fatalf("second Discard() error = %v", err)
	}
	writer, err := NewStore(root).AcquireWriter(record.ID)
	if err != nil {
		t.Fatalf("AcquireWriter() after Discard error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close() error = %v", err)
	}
}

func TestLiveThreadRejectsMismatchedRecord(t *testing.T) {
	guard, err := OpenLiveThread(NewStore(t.TempDir()), &Record{ID: "thread-live"}, false)
	if err != nil {
		t.Fatalf("OpenLiveThread() error = %v", err)
	}
	liveThread := guard.Commit()
	defer func() { _ = liveThread.Close() }()
	if err := liveThread.Save(&Record{ID: "thread-other"}); !errors.Is(err, ErrInvalidThreadID) {
		t.Fatalf("Save(mismatched record) error = %v, want ErrInvalidThreadID", err)
	}
}

func TestLiveThreadWithoutWriterReportsNoOwnership(t *testing.T) {
	guard, err := OpenLiveThread(NewStore(t.TempDir()), &Record{ID: "thread-legacy", Metadata: Metadata{HistoryMode: "legacy"}}, false)
	if err != nil {
		t.Fatalf("OpenLiveThread() error = %v", err)
	}
	liveThread := guard.Commit()
	defer func() { _ = liveThread.Close() }()
	if liveThread.OwnsWriter() {
		t.Fatal("legacy live thread unexpectedly owns a writer")
	}
}
