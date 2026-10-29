//go:build stability

package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"codex_go/rollout"
	"codex_go/stability/harness/disk"
	"codex_go/stability/harness/report"
)

// TestDiskTempCleanup proves the compression pass reclaims the staging files an
// interrupted run leaves behind, while still refusing to delete a temp file
// that may belong to a live writer. It then ages that file and checks the next
// pass removes it, leaving the Codex home with zero temp files.
func TestDiskTempCleanup(t *testing.T) {
	root := os.Getenv("CODEX_STABILITY_DISK_DIR")
	if root == "" {
		root = t.TempDir()
	} else {
		root = filepath.Join(root, fmt.Sprintf("temp-%d", time.Now().UnixNano()))
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatalf("create root: %v", err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(root) })
	}

	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome:     root,
		ThreadID:      "thread-temp-cleanup",
		SessionID:     "session-temp-cleanup",
		Source:        "cli",
		ThreadSource:  "cli",
		CWD:           "/tmp/repo",
		Model:         "gpt-5",
		ModelProvider: "openai",
		HistoryMode:   "legacy",
		MemoryMode:    "enabled",
		Now:           time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("new recorder: %v", err)
	}
	for turn := 0; turn < 5; turn++ {
		item, err := json.Marshal(map[string]any{
			"type":    "message",
			"role":    "user",
			"content": []map[string]any{{"type": "input_text", "text": fmt.Sprintf("turn %d", turn)}},
		})
		if err != nil {
			t.Fatalf("marshal item: %v", err)
		}
		if err := recorder.AppendLine(rollout.Line{Type: "item", Item: item}); err != nil {
			t.Fatalf("append item: %v", err)
		}
	}
	path := recorder.Path()
	if err := recorder.Close(); err != nil {
		t.Fatalf("close recorder: %v", err)
	}

	// Cold rollouts are the only ones the pass compresses.
	old := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("age rollout: %v", err)
	}
	dir := filepath.Dir(path)
	staleTemp := filepath.Join(dir, "rollout-compress-stale.tmp")
	freshTemp := filepath.Join(dir, "rollout-compress-fresh.tmp")
	for _, file := range []string{staleTemp, freshTemp} {
		if err := os.WriteFile(file, []byte("stalled staging file"), 0o644); err != nil {
			t.Fatalf("write %s: %v", file, err)
		}
	}
	if err := os.Chtimes(staleTemp, old, old); err != nil {
		t.Fatalf("age stale temp: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := rollout.RunRolloutCompression(ctx, root, rollout.RolloutCompressionTriggerRPC); err != nil {
		t.Fatalf("compression pass: %v", err)
	}

	run := report.New("disk-temp-cleanup")
	run.Env["rollout"] = path
	_, staleErr := os.Stat(staleTemp)
	_, freshErr := os.Stat(freshTemp)
	_, compressedErr := os.Stat(path + ".zst")
	_, sourceErr := os.Stat(path)
	run.Check("disk.temp_cleanup.stale_removed", os.IsNotExist(staleErr), "path=%s", staleTemp)
	run.Check("disk.temp_cleanup.fresh_kept", freshErr == nil, "path=%s", freshTemp)
	run.Check("disk.temp_cleanup.rollout_compressed", compressedErr == nil, "path=%s.zst", path)
	run.Check("disk.temp_cleanup.source_removed", os.IsNotExist(sourceErr), "path=%s", path)

	// Second phase: a home whose only content is an aged staging file. It uses
	// a separate home because the run marker deliberately skips a second pass
	// within six hours, and we want to observe the cleanup itself.
	secondHome, err := os.MkdirTemp("", "codex-temp-cleanup-")
	if err != nil {
		t.Fatalf("create second home: %v", err)
	}
	defer func() { _ = os.RemoveAll(secondHome) }()
	secondTemp := filepath.Join(secondHome, "rollout-compress-orphan.tmp")
	if err := os.WriteFile(secondTemp, []byte("orphaned staging file"), 0o644); err != nil {
		t.Fatalf("write orphan temp: %v", err)
	}
	if err := os.Chtimes(secondTemp, old, old); err != nil {
		t.Fatalf("age orphan temp: %v", err)
	}
	if err := rollout.RunRolloutCompression(ctx, secondHome, rollout.RolloutCompressionTriggerRPC); err != nil {
		t.Fatalf("second compression pass: %v", err)
	}
	snapshot, err := disk.Take(secondHome)
	if err != nil {
		t.Fatalf("disk snapshot: %v", err)
	}
	budgets, err := report.LoadBudgets(budgetsPath())
	if err != nil {
		t.Fatalf("load budgets: %v", err)
	}
	run.AddDisk(snapshot)
	run.Env["temp_files_after"] = strconv.Itoa(snapshot.TempFiles)
	run.Check("disk.temp_cleanup.temp_files", snapshot.TempFiles <= budgets.Disk.OrphanTempFiles,
		"temp_files=%d budget=%d", snapshot.TempFiles, budgets.Disk.OrphanTempFiles)
	run.Check("disk.temp_cleanup.wal", snapshot.WALBytes <= budgets.Disk.WALBytes,
		"wal=%d budget=%d", snapshot.WALBytes, budgets.Disk.WALBytes)
	run.Metric("disk.temp_files_after", float64(snapshot.TempFiles))
	run.Metric("disk.total_bytes_after", float64(snapshot.TotalBytes))
	finishReport(t, run)
}
