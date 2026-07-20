package history

import (
	"os"
	"testing"
)

func TestMessageHistoryAppendMetadataAndLookup(t *testing.T) {
	config := NewMessageHistoryConfig(t.TempDir(), MessageHistoryPersistenceSaveAll, nil)
	if err := AppendMessageHistoryEntry("hello", "session-1", config); err != nil {
		t.Fatalf("AppendMessageHistoryEntry() error = %v", err)
	}
	logID, count := MessageHistoryMetadata(config)
	if logID == 0 || count != 1 {
		t.Fatalf("MessageHistoryMetadata() = %d, %d", logID, count)
	}
	entry, ok := LookupMessageHistoryEntry(logID, 0, config)
	if !ok || entry.Text != "hello" || entry.SessionID != "session-1" {
		t.Fatalf("LookupMessageHistoryEntry() = %+v, %v", entry, ok)
	}
}

func TestAppendSkipsPersistenceNone(t *testing.T) {
	config := NewMessageHistoryConfig(t.TempDir(), MessageHistoryPersistenceNone, nil)
	if err := AppendMessageHistoryEntry("hello", "session-1", config); err != nil {
		t.Fatalf("AppendMessageHistoryEntry() error = %v", err)
	}
	if _, err := os.Stat(config.HistoryPath()); !os.IsNotExist(err) {
		t.Fatalf("history file exists or unexpected err: %v", err)
	}
}

func TestTrimTargetBytes(t *testing.T) {
	if got := MessageHistoryTrimTargetBytes(100, 20); got != 80 {
		t.Fatalf("MessageHistoryTrimTargetBytes() = %d", got)
	}
	if got := MessageHistoryTrimTargetBytes(100, 120); got != 120 {
		t.Fatalf("MessageHistoryTrimTargetBytes(newest) = %d", got)
	}
}

func TestLookupMessageHistoryEntriesReadsOffsetsInOneBatch(t *testing.T) {
	config := NewMessageHistoryConfig(t.TempDir(), MessageHistoryPersistenceSaveAll, nil)
	for _, text := range []string{"zero", "one", "two", "three"} {
		if err := AppendMessageHistoryEntry(text, "session", config); err != nil {
			t.Fatal(err)
		}
	}
	logID, _ := MessageHistoryMetadata(config)
	entries := LookupMessageHistoryEntries(logID, []int{3, 1, 3, -1, 99}, config)
	if len(entries) != 2 || entries[1].Text != "one" || entries[3].Text != "three" {
		t.Fatalf("entries = %#v", entries)
	}
}
