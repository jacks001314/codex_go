package rollout

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSessionIndexLatestEntryAndValidEOFMatchRust(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, SessionIndexFilename)
	contents := "{\"id\":\"thread-1\",\"thread_name\":\"first\",\"updated_at\":\"2024-01-01T00:00:00Z\"}\n" +
		"not-json\n" +
		"{\"id\":\"thread-1\",\"thread_name\":\"second\",\"updated_at\":\"2024-01-02T00:00:00Z\"}"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	name, found, err := FindThreadNameByID(home, "thread-1")
	if err != nil || !found || name != "second" {
		t.Fatalf("FindThreadNameByID() = %q, %v, %v", name, found, err)
	}
	names, err := FindThreadNamesByIDs(home, map[string]struct{}{"thread-1": {}, "missing": {}})
	if err != nil || len(names) != 1 || names["thread-1"] != "second" {
		t.Fatalf("FindThreadNamesByIDs() = %#v, %v", names, err)
	}
}

func TestFindThreadMetaByNameSkipsUnsavedPartialAndHistoricalEntries(t *testing.T) {
	home := t.TempDir()
	savedPath := writeNamedIndexRollout(t, home, "saved", time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	partialPath := PathForThread(home, "partial", time.Date(2024, 1, 1, 0, 0, 1, 0, time.UTC))
	if err := os.MkdirAll(filepath.Dir(partialPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(partialPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []SessionIndexEntry{
		{ID: "renamed", ThreadName: "same", UpdatedAt: "2024-01-01T00:00:00Z"},
		{ID: "saved", ThreadName: "same", UpdatedAt: "2024-01-02T00:00:00Z"},
		{ID: "partial", ThreadName: "same", UpdatedAt: "2024-01-03T00:00:00Z"},
		{ID: "missing", ThreadName: "same", UpdatedAt: "2024-01-04T00:00:00Z"},
		{ID: "renamed", ThreadName: "different", UpdatedAt: "2024-01-05T00:00:00Z"},
	} {
		if err := AppendSessionIndexEntry(home, entry); err != nil {
			t.Fatal(err)
		}
	}
	path, meta, found, err := FindThreadMetaByName(home, "same")
	if err != nil || !found || path != savedPath || meta == nil || meta.ID != "saved" {
		t.Fatalf("FindThreadMetaByName() = path:%q meta:%#v found:%v err:%v", path, meta, found, err)
	}
}

func TestRemoveThreadNameEntriesPreservesOtherAndMalformedLines(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, SessionIndexFilename)
	contents := "{\"id\":\"remove\",\"thread_name\":\"old\",\"updated_at\":\"1\"}\nnot-json\n{\"id\":\"keep\",\"thread_name\":\"kept\",\"updated_at\":\"2\"}\n{\"id\":\"remove\",\"thread_name\":\"new\",\"updated_at\":\"3\"}\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RemoveThreadNameEntries(home, "remove"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "not-json\n{\"id\":\"keep\",\"thread_name\":\"kept\",\"updated_at\":\"2\"}\n"
	if string(data) != want {
		t.Fatalf("remaining index = %q, want %q", data, want)
	}
}

func writeNamedIndexRollout(t *testing.T, home, threadID string, now time.Time) string {
	t.Helper()
	recorder, err := NewRecorder(&CreateParams{CodexHome: home, ThreadID: threadID, Source: "cli", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	return recorder.Path()
}

func TestFindThreadMetaCandidatesSortsByMtimeAndFiltersLikeRust(t *testing.T) {
	home := t.TempDir()
	olderPath := writeNamedIndexRollout(t, home, "thread-older", time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	newerPath := writeNamedIndexRollout(t, home, "thread-newer", time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	base := time.Date(2024, 2, 1, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(olderPath, base, base.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newerPath, base, base); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []SessionIndexEntry{
		{ID: "thread-older", ThreadName: "same", UpdatedAt: "2024-01-01T00:00:00Z"},
		{ID: "thread-newer", ThreadName: "same", UpdatedAt: "2024-01-02T00:00:00Z"},
	} {
		if err := AppendSessionIndexEntry(home, entry); err != nil {
			t.Fatal(err)
		}
	}

	// The most recently modified eligible legacy duplicate wins (Rust c38a60ded2).
	path, meta, found, err := FindThreadMetaByName(home, "same")
	if err != nil || !found || path != newerPath || meta == nil || meta.ID != "thread-newer" {
		t.Fatalf("FindThreadMetaByName() = path:%q meta:%#v found:%v err:%v; want newest mtime", path, meta, found, err)
	}

	// Source filtering rejects every cli rollout when only chatgpt is allowed.
	filtered, err := FindThreadMetaCandidatesByNameInCollection(home, "same", false, []string{"chatgpt"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 0 {
		t.Fatalf("source-filtered candidates = %#v, want none", filtered)
	}

	// Provider filtering rejects only explicit mismatches; empty providers pass.
	recorder, err := NewRecorder(&CreateParams{
		CodexHome: home, ThreadID: "thread-provider", Source: "cli", ModelProvider: "bedrock", Now: base,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if err := AppendSessionIndexEntry(home, SessionIndexEntry{ID: "thread-provider", ThreadName: "provider-named", UpdatedAt: "2024-02-01T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	onlyOpenAI, err := FindThreadMetaCandidatesByNameInCollection(home, "provider-named", false, nil, []string{"openai"})
	if err != nil {
		t.Fatal(err)
	}
	if len(onlyOpenAI) != 0 {
		t.Fatalf("provider-filtered candidates = %#v, want none for openai-only", onlyOpenAI)
	}
	withBedrock, err := FindThreadMetaCandidatesByNameInCollection(home, "provider-named", false, nil, []string{"bedrock"})
	if err != nil {
		t.Fatal(err)
	}
	if len(withBedrock) != 1 || withBedrock[0].Meta == nil || withBedrock[0].Meta.ID != "thread-provider" {
		t.Fatalf("provider-filtered candidates = %#v, want the bedrock rollout", withBedrock)
	}
}

// TestFindThreadNamesByIDsScansBackwardsLikeRust mirrors Rust #49297: the batch
// lookup resolves each id to its latest nonempty, trimmed name, skipping
// whitespace-only names, malformed entries, and a partial final write.
func TestFindThreadNamesByIDsScansBackwardsLikeRust(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, SessionIndexFilename)
	contents := "{\"id\":\"renamed\",\"thread_name\":\"old\",\"updated_at\":\"1\"}\n" +
		"not-json\n" +
		"{\"id\":\"renamed\",\"thread_name\":\"  latest  \",\"updated_at\":\"2\"}\n" +
		"{\"id\":\"blank\",\"thread_name\":\"real\",\"updated_at\":\"3\"}\n" +
		"{\"id\":\"blank\",\"thread_name\":\"   \",\"updated_at\":\"4\"}\n" +
		"{\"id\":\"unicode\",\"thread_name\":\"  主题  \",\"updated_at\":\"5\"}\n" +
		"{\"id\":\"partial\",\"thread_name\":\"trunc"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	names, err := FindThreadNamesByIDs(home, map[string]struct{}{
		"renamed": {}, "blank": {}, "unicode": {}, "partial": {}, "missing": {},
	})
	if err != nil {
		t.Fatalf("FindThreadNamesByIDs() error = %v", err)
	}
	if len(names) != 3 {
		t.Fatalf("names = %#v", names)
	}
	if names["renamed"] != "latest" {
		t.Fatalf("renamed = %q, want the trimmed latest name", names["renamed"])
	}
	if names["blank"] != "real" {
		t.Fatalf("blank = %q, want the earlier nonempty name", names["blank"])
	}
	if names["unicode"] != "主题" {
		t.Fatalf("unicode = %q", names["unicode"])
	}
	if _, ok := names["partial"]; ok {
		t.Fatalf("partial entry should be skipped: %#v", names)
	}
	if _, ok := names["missing"]; ok {
		t.Fatalf("missing id should be absent: %#v", names)
	}
}

// TestAppendAndRemoveThreadNamesPreserveOtherEntries mirrors Rust #49959:
// appending an original and an updated name for one thread alongside another
// thread's entry must leave the lookup resolving the updated name, and removing
// the first thread's name entries must keep only the other thread's entry.
func TestAppendAndRemoveThreadNamesPreserveOtherEntries(t *testing.T) {
	home := t.TempDir()
	removedID := "thread-removed"
	retained := SessionIndexEntry{
		ID:         "thread-retained",
		ThreadName: "retained",
		UpdatedAt:  "2024-01-01T00:00:00Z",
	}
	for _, entry := range []SessionIndexEntry{
		{ID: removedID, ThreadName: "original", UpdatedAt: retained.UpdatedAt},
		retained,
		{ID: removedID, ThreadName: "renamed", UpdatedAt: retained.UpdatedAt},
	} {
		if err := AppendSessionIndexEntry(home, entry); err != nil {
			t.Fatalf("AppendSessionIndexEntry(%q) error = %v", entry.ThreadName, err)
		}
	}

	name, found, err := FindThreadNameByID(home, removedID)
	if err != nil || !found || name != "renamed" {
		t.Fatalf("FindThreadNameByID() = %q, %v, %v; want the latest appended name", name, found, err)
	}

	if err := RemoveThreadNameEntries(home, removedID); err != nil {
		t.Fatalf("RemoveThreadNameEntries() error = %v", err)
	}
	data, err := os.ReadFile(sessionIndexPath(home))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	line, err := json.Marshal(retained)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if want := string(line) + "\n"; string(data) != want {
		t.Fatalf("remaining session index = %q, want %q", data, want)
	}
}
