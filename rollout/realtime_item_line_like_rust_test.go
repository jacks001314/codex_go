package rollout

// Pins the Rust RolloutItem::RealtimeItem wire shape: a `realtime_item` record
// whose `payload` is the canonical snake_case core item (#41924).

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestAppendRealtimeItemsWritesCanonicalRolloutRecordsLikeRust(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	recorder, err := NewRecorder(&CreateParams{
		CodexHome: home, SessionID: "thread-1", ThreadID: "thread-1", CWD: home,
		ModelProvider: "openai", HistoryMode: "paginated", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	path := recorder.Path()
	items := []json.RawMessage{
		json.RawMessage(`{"id":"voice-1:started","realtime_session_id":"voice-1","type":"realtime_session_started"}`),
		json.RawMessage(`{"id":"voice-1:transcript","realtime_session_id":"voice-1","type":"transcript_segment","role":"assistant","text":"hi"}`),
	}
	if err := AppendRealtimeItems(recorder, items, now); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	var realtimeLines []Line
	for _, line := range lines {
		var decoded Line
		if err := json.Unmarshal([]byte(line), &decoded); err != nil {
			t.Fatalf("decode rollout line: %v", err)
		}
		if decoded.Type == "realtime_item" {
			realtimeLines = append(realtimeLines, decoded)
		}
	}
	if len(realtimeLines) != 2 {
		t.Fatalf("realtime_item lines = %d, want 2", len(realtimeLines))
	}
	for index, line := range realtimeLines {
		if string(line.Payload) != string(items[index]) {
			t.Fatalf("line %d payload = %s, want %s", index, line.Payload, items[index])
		}
		if line.Timestamp != now.Format(time.RFC3339Nano) {
			t.Fatalf("line %d timestamp = %q", index, line.Timestamp)
		}
	}

	loaded, parseErrors, err := Load(path)
	if err != nil || parseErrors != 0 {
		t.Fatalf("Load() error = %v parse errors = %d", err, parseErrors)
	}
	count := 0
	for _, line := range loaded {
		if line.Type != "realtime_item" {
			continue
		}
		if string(line.RealtimeItem) != string(items[count]) {
			t.Fatalf("loaded realtime item %d = %s", count, line.RealtimeItem)
		}
		count++
	}
	if count != 2 {
		t.Fatalf("loaded realtime items = %d, want 2", count)
	}
}
