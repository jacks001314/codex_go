//go:build stability

package scenarios

import (
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

// TestDiskGrowth1KTurns drives 1000 turns through the real rollout recorder and
// bounds how many bytes a session writes per 1000 turns. It also asserts the
// recorder leaves no temporary files behind.
func TestDiskGrowth1KTurns(t *testing.T) {
	run := report.New("disk-growth")
	turns := envIntDefault("CODEX_STABILITY_DISK_TURNS", 1000)
	root := os.Getenv("CODEX_STABILITY_DISK_DIR")
	if root == "" {
		root = t.TempDir()
	} else {
		root = filepath.Join(root, fmt.Sprintf("run-%d", time.Now().UnixNano()))
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatalf("create disk root: %v", err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(root) })
	}

	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome:     root,
		ThreadID:      "thread-disk-growth",
		SessionID:     "session-disk-growth",
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

	payloadBytes := envIntDefault("CODEX_STABILITY_DISK_PAYLOAD", 1024)
	payload := make([]byte, payloadBytes)
	for index := range payload {
		payload[index] = 'a'
	}
	start := time.Now()
	for turn := 0; turn < turns; turn++ {
		if err := recorder.AppendLine(rollout.Line{
			Type:    "turn_context",
			Payload: json.RawMessage(fmt.Sprintf(`{"turn":%d,"cwd":"/tmp/repo"}`, turn)),
		}); err != nil {
			t.Fatalf("append turn_context %d: %v", turn, err)
		}
		for item := 0; item < 4; item++ {
			itemJSON, err := json.Marshal(map[string]any{
				"type": "message",
				"role": "user",
				"content": []map[string]any{{
					"type": "input_text",
					"text": fmt.Sprintf("turn %d item %d %s", turn, item, string(payload)),
				}},
			})
			if err != nil {
				t.Fatalf("marshal item: %v", err)
			}
			if err := recorder.AppendLine(rollout.Line{
				Type:      "item",
				TurnID:    fmt.Sprintf("turn-%06d", turn),
				ItemID:    fmt.Sprintf("item-%06d-%02d", turn, item),
				Item:      itemJSON,
				Timestamp: time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
			}); err != nil {
				t.Fatalf("append item %d/%d: %v", turn, item, err)
			}
		}
	}
	elapsed := time.Since(start)
	path := recorder.Path()
	if err := recorder.Close(); err != nil {
		t.Fatalf("close recorder: %v", err)
	}

	snapshot, err := disk.Take(root)
	if err != nil {
		t.Fatalf("disk snapshot: %v", err)
	}
	budgets, err := report.LoadBudgets(budgetsPath())
	if err != nil {
		t.Fatalf("load budgets: %v", err)
	}
	perThousand := float64(snapshot.TotalBytes) * 1000 / float64(turns)

	run.Env["turns"] = strconv.Itoa(turns)
	run.Env["payload_bytes"] = strconv.Itoa(payloadBytes)
	run.Env["elapsed"] = elapsed.String()
	run.Env["rollout"] = path
	run.AddDisk(snapshot)
	run.Check("disk.bytes_per_1k_turns", perThousand <= float64(budgets.Disk.BytesPer1KTurns),
		"%.0f bytes/1k turns (budget %d) for %d turns", perThousand, budgets.Disk.BytesPer1KTurns, turns)
	run.Check("disk.orphan_temp_files", snapshot.TempFiles <= budgets.Disk.OrphanTempFiles,
		"temp_files=%d budget=%d", snapshot.TempFiles, budgets.Disk.OrphanTempFiles)
	run.Check("disk.wal_bytes", snapshot.WALBytes <= budgets.Disk.WALBytes,
		"wal=%d budget=%d", snapshot.WALBytes, budgets.Disk.WALBytes)
	run.Metric("disk.total_bytes", float64(snapshot.TotalBytes))
	run.Metric("disk.bytes_per_1k_turns", perThousand)
	run.Metric("disk.wal_bytes", float64(snapshot.WALBytes))
	finishReport(t, run)
}

func envIntDefault(name string, fallback int) int {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}
