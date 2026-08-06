package rollout

import (
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
