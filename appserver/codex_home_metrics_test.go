package appserver

import (
	"os"
	"path/filepath"
	"sync/atomic"
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

	sizes, err := scanCodexHomeSizes(home, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sizes.sessions != 20 || sizes.archivedSessions != 30 {
		t.Fatalf("sizes = %+v", sizes)
	}

	metrics := state.NewTaskMetrics()
	recordCodexHomeMetrics(metrics, home, nil)
	records := metrics.Records()
	if len(records) != 2 {
		t.Fatalf("records = %#v", records)
	}
	found := map[string]int{}
	for _, record := range records {
		if record.Name != codexHomeSizeBytesMetric || record.Kind != "histogram" || len(record.Boundaries) != 7 {
			t.Fatalf("metric = %+v", record)
		}
		found[record.Tags["directory"]] = record.Value
	}
	if found[rollout.SessionsSubdir] != 20 || found[rollout.ArchivedSessionsSubdir] != 30 {
		t.Fatalf("metrics = %#v", found)
	}
	if _, ok := found["codex_home"]; ok {
		t.Fatalf("aggregate codex_home sample is still emitted: %#v", found)
	}
}

func TestCodexHomeMetricsSkipsMissingAndSymlinkedSessionRootsLikeRust(t *testing.T) {
	home := t.TempDir()
	// Missing sessions directories are skipped rather than failing the scan.
	sizes, err := scanCodexHomeSizes(home, nil)
	if err != nil {
		t.Fatalf("scanCodexHomeSizes error = %v", err)
	}
	if sizes.sessions != 0 || sizes.archivedSessions != 0 {
		t.Fatalf("sizes = %+v", sizes)
	}
	// A symlinked sessions root must not be followed.
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, "s.jsonl"), make([]byte, 40), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(home, rollout.SessionsSubdir)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	sizes, err = scanCodexHomeSizes(home, nil)
	if err != nil {
		t.Fatalf("scanCodexHomeSizes error = %v", err)
	}
	if sizes.sessions != 0 {
		t.Fatalf("sizes = %+v, symlinked sessions root must not be followed", sizes)
	}
}

func TestCodexHomeMetricsCancelAbortsScanLikeRust(t *testing.T) {
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
	cancelled := int32(1)
	_, err := scanCodexHomeSizes(home, func() bool { return atomic.LoadInt32(&cancelled) == 1 })
	if err == nil {
		t.Fatal("cancelled scan returned no error")
	}
	metrics := state.NewTaskMetrics()
	recordCodexHomeMetrics(metrics, home, func() bool { return true })
	if len(metrics.Records()) != 0 {
		t.Fatalf("cancelled metric records = %#v", metrics.Records())
	}
}
