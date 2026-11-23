package rollout

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeScanMarkerRollout writes session metadata, junkRecord-1 unrelated
// records, then lastLine.
func writeScanMarkerRollout(t *testing.T, junkRecord int, lastLine string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rollout-2026-06-29T01-02-03-thread-marker.jsonl")
	lines := []string{
		`{"timestamp":"2026-06-29T01:02:03Z","type":"session_meta","payload":{"id":"thread-marker","timestamp":"2026-06-29T01:02:03Z","source":"cli","model_provider":"openai","cwd":"/repo"}}`,
	}
	for len(lines) < junkRecord-1 {
		lines = append(lines, `{"timestamp":"2026-06-29T01:02:04Z","type":"turn_context","payload":{"cwd":"/repo","response_id":"resp-unrelated"}}`)
	}
	lines = append(lines, lastLine)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

// Rust #62636 skips JSON parsing for lines without the event marker, but a
// Unicode escape can spell part of the tag, so escaped events still decode.
func TestBuildThreadItemDecodesEscapedEventTagAfterMetadataLikeRust(t *testing.T) {
	path := writeScanMarkerRollout(t, 30, `{"timestamp":"2026-06-29T01:02:05Z","type":"\u0065vent_msg","payload":{"type":"user_message","message":"escaped preview"}}`)
	item, ok := BuildThreadItem(path, false)
	if !ok {
		t.Fatal("BuildThreadItem() ok = false")
	}
	if item.FirstUserMessage != "escaped preview" || item.Preview != "escaped preview" {
		t.Fatalf("BuildThreadItem() preview = %q/%q, want escaped preview", item.Preview, item.FirstUserMessage)
	}
}

// Unrelated records no longer need a JSON decode, but the scan budget still
// counts them (Rust lines_scanned increments before the marker check).
func TestBuildThreadItemSkipsUnrelatedRecordsAfterMetadataLikeRust(t *testing.T) {
	path := writeScanMarkerRollout(t, 30, `{"timestamp":"2026-06-29T01:02:05Z","type":"event_msg","payload":{"type":"user_message","message":"plain preview"}}`)
	item, ok := BuildThreadItem(path, false)
	if !ok {
		t.Fatal("BuildThreadItem() ok = false")
	}
	if item.FirstUserMessage != "plain preview" || item.Preview != "plain preview" {
		t.Fatalf("BuildThreadItem() preview = %q/%q, want plain preview", item.Preview, item.FirstUserMessage)
	}
	if item.LastResponseID != "" {
		t.Fatalf("unrelated record response id = %q, want the event-only summary", item.LastResponseID)
	}
}

func TestBuildThreadItemEscapedEventRespectsScanLimitLikeRust(t *testing.T) {
	path := writeScanMarkerRollout(
		t,
		threadListItemHeadLimit+threadListItemUserScanLimit+8,
		`{"timestamp":"2026-06-29T01:02:05Z","type":"\u0065vent_msg","payload":{"type":"user_message","message":"too late"}}`,
	)
	item, ok := BuildThreadItem(path, false)
	if !ok {
		t.Fatal("BuildThreadItem() ok = false")
	}
	if item.FirstUserMessage != "" || item.Preview != "" {
		t.Fatalf("BuildThreadItem() preview = %q/%q, want empty beyond the scan cap", item.Preview, item.FirstUserMessage)
	}
}

// Rust #62636 extends coverage for archived listings filtered by provider and
// sorted by update time.
func TestListThreadsArchivedProviderFilterWithUpdatedAtSortLikeRust(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ArchivedSessionsSubdir, "2026", "11", "17")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "rollout-2026-11-17T00-00-00-thread-archived.jsonl")
	lines := []string{
		`{"timestamp":"2026-11-17T00:00:00Z","type":"session_meta","payload":{"id":"thread-archived","timestamp":"2026-11-17T00:00:00Z","cwd":"/repo","source":"cli","model_provider":"mock_provider"}}`,
		`{"timestamp":"2026-11-17T00:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"archived hello"}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		sortKey  SortKey
		provider string
		want     int
	}{
		{sortKey: "", provider: "mock_provider", want: 1},
		{sortKey: SortUpdatedAt, provider: "mock_provider", want: 1},
		{sortKey: SortUpdatedAt, provider: "other_provider", want: 0},
	} {
		page, err := ListThreads(home, ListOptions{
			Archived:       true,
			SortKey:        testCase.sortKey,
			ModelProviders: []string{testCase.provider},
			PageSize:       10,
		})
		if err != nil {
			t.Fatalf("ListThreads(%v, %s) error = %v", testCase.sortKey, testCase.provider, err)
		}
		if len(page.Items) != testCase.want {
			t.Fatalf("ListThreads(%v, %s) items = %#v, want %d", testCase.sortKey, testCase.provider, page.Items, testCase.want)
		}
		if testCase.want == 1 && (page.Items[0].ThreadID != "thread-archived" || !page.Items[0].Archived) {
			t.Fatalf("archived item = %#v", page.Items[0])
		}
	}
}
