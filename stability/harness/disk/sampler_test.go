package disk_test

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/stability/harness/disk"
)

func TestTakeCountsFilesWALAndTemp(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "state.db"), 100)
	writeFile(t, filepath.Join(root, "state.db-wal"), 40)
	writeFile(t, filepath.Join(root, "state.db-shm"), 10)
	writeFile(t, filepath.Join(root, ".tmp-orphan"), 7)
	writeFile(t, filepath.Join(root, "rollout.jsonl"), 200)

	snapshot, err := disk.Take(root)
	if err != nil {
		t.Fatalf("Take: %v", err)
	}
	if snapshot.Files != 5 {
		t.Fatalf("files = %d, want 5", snapshot.Files)
	}
	if snapshot.TotalBytes != 357 {
		t.Fatalf("total bytes = %d, want 357", snapshot.TotalBytes)
	}
	if snapshot.WALBytes != 40 {
		t.Fatalf("wal bytes = %d, want 40", snapshot.WALBytes)
	}
	if snapshot.SHMBytes != 10 {
		t.Fatalf("shm bytes = %d, want 10", snapshot.SHMBytes)
	}
	if snapshot.TempFiles != 1 || snapshot.TempBytes != 7 {
		t.Fatalf("temp files=%d bytes=%d, want 1/7", snapshot.TempFiles, snapshot.TempBytes)
	}
}

func TestDeltaReportsGrowth(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a"), 10)
	before, err := disk.Take(root)
	if err != nil {
		t.Fatalf("Take before: %v", err)
	}
	writeFile(t, filepath.Join(root, "b"), 5)
	after, err := disk.Take(root)
	if err != nil {
		t.Fatalf("Take after: %v", err)
	}
	delta := after.Delta(before)
	if delta.TotalBytes != 5 || delta.Files != 1 {
		t.Fatalf("delta = %+v, want +5 bytes and +1 file", delta)
	}
}

func writeFile(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
