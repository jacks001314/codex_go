package appserver

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/rollout"
	"codex_go/state"
)

func TestCodexHomeMetricsScanLikeRust(t *testing.T) {
	home := t.TempDir()
	writeFile := func(rel string, size int) {
		path := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeFile("config.toml", 10)
	writeFile(filepath.Join(rollout.SessionsSubdir, "s.jsonl"), 20)
	writeFile(filepath.Join(rollout.ArchivedSessionsSubdir, "a.jsonl"), 30)
	writeFile(filepath.Join("other", "note.txt"), 5)

	sizes, err := scanCodexHomeSizes(home)
	if err != nil {
		t.Fatal(err)
	}
	if sizes.codexHome != 65 || sizes.sessions != 20 || sizes.archivedSessions != 30 {
		t.Fatalf("sizes = %+v", sizes)
	}

	metrics := state.NewTaskMetrics()
	recordCodexHomeMetrics(metrics, home)
	records := metrics.Records()
	if len(records) != 3 {
		t.Fatalf("records = %#v", records)
	}
	found := map[string]int{}
	for _, record := range records {
		if record.Name != codexHomeSizeBytesMetric || record.Kind != "histogram" || len(record.Boundaries) != 7 {
			t.Fatalf("metric = %+v", record)
		}
		found[record.Tags["directory"]] = record.Value
	}
	if found["codex_home"] != 65 || found[rollout.SessionsSubdir] != 20 || found[rollout.ArchivedSessionsSubdir] != 30 {
		t.Fatalf("metrics = %#v", found)
	}
}
