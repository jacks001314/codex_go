package rollout

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExternalAgentSessionRecordPreservesSourceTimestamps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	body := `{"type":"user","cwd":"/repo","timestamp_ms":1704164645000,"message":{"content":"<user_query>hello</user_query>"}}
{"type":"user","cwd":"/repo","timestamp":"2023-01-01T00:00:00Z","isMeta":true,"message":{"content":"hidden"}}
{"type":"assistant","cwd":"/repo","timestamp":"2024-03-01T04:05:06Z","message":{"content":[{"type":"text","text":"world"}]}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	record, err := ExternalAgentSessionRecord(path, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if record.CreatedAt.Format(time.RFC3339) != "2024-01-02T03:04:05Z" ||
		record.UpdatedAt.Format(time.RFC3339) != "2024-03-01T04:05:06Z" ||
		!record.RecencyAt.Equal(record.UpdatedAt) {
		t.Fatalf("timestamps = %v %v %v", record.CreatedAt, record.UpdatedAt, record.RecencyAt)
	}
	if len(record.Items) != 2 || record.Items[0].Text != "hello" || record.Items[1].Text != "world" {
		t.Fatalf("items = %#v", record.Items)
	}
}
