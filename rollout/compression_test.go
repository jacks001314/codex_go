package rollout

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRolloutCompressionWorkerCompressesColdPlainRollouts mirrors Rust's
// `rollout_compress_runs_after_startup_with_compression_disabled`: the worker
// compresses a cold plain rollout into its `.jsonl.zst` representation and
// removes the plain file, while fresh rollouts and already-compressed rollouts
// are left alone.
func TestRolloutCompressionWorkerCompressesColdPlainRollouts(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, SessionsSubdir)
	coldPath := filepath.Join(root, "rollout-2025-01-01T00-00-00-cold.jsonl")
	freshPath := filepath.Join(root, "rollout-2025-01-01T00-00-00-fresh.jsonl")
	writeTestRollout(t, coldPath, "cold", "")
	writeTestRollout(t, freshPath, "fresh", "")
	coldTime := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(coldPath, coldTime, coldTime); err != nil {
		t.Fatalf("Chtimes(%s): %v", coldPath, err)
	}

	if err := RunRolloutCompression(context.Background(), home, RolloutCompressionTriggerRPC); err != nil {
		t.Fatalf("RunRolloutCompression() error = %v", err)
	}

	compressed := coldPath + ".zst"
	if _, err := os.Stat(compressed); err != nil {
		t.Fatalf("compressed rollout missing: %v", err)
	}
	if _, err := os.Stat(coldPath); !os.IsNotExist(err) {
		t.Fatalf("plain cold rollout still present (err=%v)", err)
	}
	if _, err := os.Stat(freshPath); err != nil {
		t.Fatalf("fresh rollout was touched: %v", err)
	}
	// The compressed rollout still round-trips.
	lines, _, err := Load(compressed)
	if err != nil {
		t.Fatalf("Load(compressed) error = %v", err)
	}
	if len(lines) == 0 {
		t.Fatal("compressed rollout has no lines")
	}
	meta, err := FirstSessionMeta(compressed)
	if err != nil {
		t.Fatalf("FirstSessionMeta(compressed) error = %v", err)
	}
	if meta.ID != "cold" {
		t.Fatalf("compressed meta id = %q, want cold", meta.ID)
	}
	// The compressed representation keeps the source's modification time so
	// cold-file decisions stay stable.
	info, err := os.Stat(compressed)
	if err != nil {
		t.Fatalf("Stat(compressed): %v", err)
	}
	if !info.ModTime().Equal(coldTime) {
		t.Fatalf("compressed mtime = %v, want %v", info.ModTime(), coldTime)
	}
}

// TestRolloutCompressionWorkerHonorsRunMarker mirrors Rust's run-marker guard:
// a recent pass keeps a second pass from doing work.
func TestRolloutCompressionWorkerHonorsRunMarker(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, SessionsSubdir)
	coldPath := filepath.Join(root, "rollout-2025-01-01T00-00-00-cold.jsonl")
	writeTestRollout(t, coldPath, "cold", "")
	coldTime := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(coldPath, coldTime, coldTime); err != nil {
		t.Fatalf("Chtimes(%s): %v", coldPath, err)
	}

	markerDir := filepath.Join(home, ".tmp")
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	markerPath := filepath.Join(markerDir, compressionRunMarkerFileName)
	if err := os.WriteFile(markerPath, []byte("pid=1\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(marker): %v", err)
	}

	if err := RunRolloutCompression(context.Background(), home, RolloutCompressionTriggerRPC); err != nil {
		t.Fatalf("RunRolloutCompression() error = %v", err)
	}
	if _, err := os.Stat(coldPath + ".zst"); !os.IsNotExist(err) {
		t.Fatalf("a fresh run marker should skip the pass (err=%v)", err)
	}
	if _, err := os.Stat(coldPath); err != nil {
		t.Fatalf("cold rollout was removed: %v", err)
	}
	if _, err := os.Stat(markerPath); err != nil {
		t.Fatalf("a skipped pass should leave the marker behind: %v", err)
	}
}

// TestRolloutCompressionWorkerSkipsAlreadyCompressedRollouts mirrors the
// no-clobber publish: an existing compressed representation wins.
func TestRolloutCompressionWorkerSkipsAlreadyCompressedRollouts(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, SessionsSubdir)
	plainPath := filepath.Join(root, "rollout-2025-01-01T00-00-00-both.jsonl")
	writeTestRollout(t, plainPath, "both", "")
	coldTime := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(plainPath, coldTime, coldTime); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	compressedPath := plainPath + ".zst"
	if err := compressRolloutToPath(plainPath, compressedPath); err != nil {
		t.Fatalf("compressRolloutToPath: %v", err)
	}
	before, err := os.ReadFile(compressedPath)
	if err != nil {
		t.Fatalf("ReadFile(compressed): %v", err)
	}

	if err := RunRolloutCompression(context.Background(), home, RolloutCompressionTriggerRPC); err != nil {
		t.Fatalf("RunRolloutCompression() error = %v", err)
	}
	after, err := os.ReadFile(compressedPath)
	if err != nil {
		t.Fatalf("ReadFile(compressed): %v", err)
	}
	if string(before) != string(after) {
		t.Fatal("the existing compressed rollout was rewritten")
	}
	if _, err := os.Stat(plainPath); err != nil {
		t.Fatalf("plain rollout was removed despite the compressed copy: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".tmp", compressionRunMarkerFileName))
	if err != nil {
		t.Fatalf("run marker missing: %v", err)
	}
	if !strings.Contains(string(data), "pid=") {
		t.Fatalf("run marker contents = %q", string(data))
	}
}
