package history

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAppendMetadataAndLookup(t *testing.T) {
	config := &Config{CodexHome: t.TempDir(), Persistence: PersistenceSaveAll}
	if err := AppendEntry("hello", "session-1", config, time.Unix(10, 0)); err != nil {
		t.Fatalf("AppendEntry() error = %v", err)
	}
	logID, count, err := Metadata(config)
	if err != nil {
		t.Fatalf("Metadata() error = %v", err)
	}
	if logID == 0 || count != 1 {
		t.Fatalf("metadata = %d %d", logID, count)
	}
	entry, ok := Lookup(logID, 0, config)
	if !ok || entry.Text != "hello" || entry.Timestamp != 10 {
		t.Fatalf("lookup = %#v %v", entry, ok)
	}
}

func TestAppendSkipsWhenPersistenceNone(t *testing.T) {
	config := &Config{CodexHome: t.TempDir(), Persistence: PersistenceNone}
	if err := AppendEntry("hello", "session-1", config, time.Unix(10, 0)); err != nil {
		t.Fatalf("AppendEntry() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(config.CodexHome, Filename)); !os.IsNotExist(err) {
		t.Fatalf("history file should not exist, err=%v", err)
	}
}

func TestAppendAndLookupLongEntryBeyondScannerDefaultLimit(t *testing.T) {
	// A single history entry can exceed bufio.Scanner's default 64KB token
	// limit (e.g. a long pasted prompt). Trimming and lookup must keep working.
	config := &Config{CodexHome: t.TempDir(), Persistence: PersistenceSaveAll, MaxBytes: 100}
	longText := strings.Repeat("x", 256*1024)
	if err := AppendEntry(longText, "session-1", config, time.Unix(10, 0)); err != nil {
		t.Fatalf("AppendEntry() error = %v", err)
	}
	logID, count, err := Metadata(config)
	if err != nil {
		t.Fatalf("Metadata() error = %v", err)
	}
	if count != 1 {
		t.Fatalf("Metadata() count = %d, want 1", count)
	}
	entry, ok := Lookup(logID, 0, config)
	if !ok || entry.Text != longText {
		t.Fatalf("Lookup() ok=%v textLen=%d, want %d", ok, len(entry.Text), len(longText))
	}
}

func TestEnforceLimitRetainsNewestEntry(t *testing.T) {
	config := &Config{CodexHome: t.TempDir(), Persistence: PersistenceSaveAll, MaxBytes: 100}
	for i := 0; i < 10; i++ {
		if err := AppendEntry("message-message-message", "session", config, time.Unix(int64(i), 0)); err != nil {
			t.Fatalf("AppendEntry(%d) error = %v", i, err)
		}
	}
	_, count, err := Metadata(config)
	if err != nil {
		t.Fatalf("Metadata() error = %v", err)
	}
	if count == 0 || count >= 10 {
		t.Fatalf("expected trimmed history count, got %d", count)
	}
}
