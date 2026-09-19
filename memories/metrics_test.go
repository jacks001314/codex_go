package memories

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/config"
	"codex_go/state"
)

// Mirrors Rust's memory_metric_tags: the reported version follows the pipeline's
// selected root, and an unset version is v1.
func TestMemoryVersionTagValueFollowsTheSelectedRoot(t *testing.T) {
	cases := map[config.MemoryVersion]string{
		config.MemoryVersionV1: "v1",
		config.MemoryVersionV2: "v2",
		"":                     "v1",
	}
	for version, want := range cases {
		if got := MemoryVersionTagValue(version); got != want {
			t.Fatalf("MemoryVersionTagValue(%q) = %q, want %q", version, got, want)
		}
	}
}

// RecordMemoryStorageSize reports the root's size on Rust's byte histogram with
// its bucket boundaries and the memory-version tag, and stays silent without a
// sink or when the root cannot be measured.
func TestRecordMemoryStorageSizeFollowsRust(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "memory_summary.md"), []byte("v1\nsummary\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	metrics := state.NewTaskMetrics()
	pipeline := &StartupPipeline{Metrics: metrics, Version: config.MemoryVersionV2}
	if !pipeline.RecordMemoryStorageSize(root) {
		t.Fatal("a measurable root must record a sample")
	}
	records := metrics.Records()
	if len(records) != 1 {
		t.Fatalf("records = %#v", records)
	}
	record := records[0]
	if record.Name != MemoryStorageBytesMetric || record.Kind != "histogram" || record.Value != len("v1\nsummary\n") {
		t.Fatalf("record = %#v", record)
	}
	if record.Tags[MemoryVersionTag] != "v2" {
		t.Fatalf("record tags = %#v", record.Tags)
	}
	// Rust stops the phase-two timer before measuring, so the sample is taken
	// after the artifacts of the run are in place; the boundaries are Rust's
	// log-spaced byte buckets.
	boundaries := MemoryStorageBytesBoundaries()
	if len(record.Boundaries) != len(boundaries) {
		t.Fatalf("boundaries = %#v", record.Boundaries)
	}
	for index := range boundaries {
		if record.Boundaries[index] != boundaries[index] {
			t.Fatalf("boundary %d = %v, want %v", index, record.Boundaries[index], boundaries[index])
		}
	}

	if (&StartupPipeline{Version: config.MemoryVersionV1}).RecordMemoryStorageSize(root) {
		t.Fatal("a pipeline without a sink must not record")
	}
	silent := state.NewTaskMetrics()
	unmeasurable := &StartupPipeline{Metrics: silent, Version: config.MemoryVersionV1}
	if unmeasurable.RecordMemoryStorageSize(filepath.Join(root, "missing")) {
		t.Fatal("an unmeasurable root must not record")
	}
	if records := silent.Records(); len(records) != 0 {
		t.Fatalf("unmeasurable records = %#v", records)
	}
}
