package state

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/rollout"
	"github.com/klauspost/compress/zstd"
)

func TestBackfillLeaseCheckpointAndCompletionMatchRust(t *testing.T) {
	runtime := newBackfillTestRuntime(t)
	ctx := context.Background()

	initial, err := runtime.GetBackfillState(ctx)
	if err != nil || initial.Status != BackfillPending || initial.LastWatermark != nil || initial.LastSuccessAt != nil {
		t.Fatalf("initial backfill state = %+v, %v", initial, err)
	}
	claimed, err := runtime.TryClaimBackfill(ctx, 3600)
	if err != nil || !claimed {
		t.Fatalf("initial claim = %v, %v", claimed, err)
	}
	claimed, err = runtime.TryClaimBackfill(ctx, 3600)
	if err != nil || claimed {
		t.Fatalf("duplicate claim = %v, %v", claimed, err)
	}
	if _, err := runtime.StateDB().ExecContext(ctx, `UPDATE backfill_state SET updated_at = 1 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	claimed, err = runtime.TryClaimBackfill(ctx, 10)
	if err != nil || !claimed {
		t.Fatalf("stale claim = %v, %v", claimed, err)
	}
	watermark := "sessions/2026/07/31/rollout-a.jsonl"
	if err := runtime.CheckpointBackfill(ctx, watermark); err != nil {
		t.Fatal(err)
	}
	if err := runtime.MarkBackfillComplete(ctx, nil); err != nil {
		t.Fatal(err)
	}
	completed, err := runtime.GetBackfillState(ctx)
	if err != nil || completed.Status != BackfillComplete || completed.LastWatermark == nil || *completed.LastWatermark != watermark || completed.LastSuccessAt == nil {
		t.Fatalf("completed backfill state = %+v, %v", completed, err)
	}
	claimed, err = runtime.TryClaimBackfill(ctx, 0)
	if err != nil || claimed {
		t.Fatalf("completed claim = %v, %v", claimed, err)
	}
}

func TestRunRolloutBackfillProjectsActiveArchivedAndCompressedMetadata(t *testing.T) {
	home := t.TempDir()
	runtime := newBackfillTestRuntimeAt(t, home)
	active := writeBackfillRollout(t, home, "active-thread", time.Date(2026, 7, 30, 1, 2, 3, 0, time.UTC), false)
	archivedSource := writeBackfillRollout(t, home, "archived-thread", time.Date(2026, 7, 29, 1, 2, 3, 0, time.UTC), false)
	archived, err := rollout.Archive(archivedSource, home)
	if err != nil {
		t.Fatal(err)
	}
	compressed := compressBackfillRollout(t, active)
	if err := os.Remove(active); err != nil {
		t.Fatal(err)
	}

	stats, claimed, err := RunRolloutBackfill(context.Background(), runtime, home, RolloutBackfillOptions{BatchSize: 1})
	if err != nil || !claimed {
		t.Fatalf("RunRolloutBackfill() = %+v, claimed=%v, err=%v", stats, claimed, err)
	}
	if stats != (BackfillStats{Scanned: 2, Upserted: 2}) {
		t.Fatalf("backfill stats = %+v", stats)
	}

	assertBackfilledThread(t, runtime, "active-thread", compressed, false, "active request")
	assertBackfilledThread(t, runtime, "archived-thread", archived, true, "archived request")
	state, err := runtime.GetBackfillState(context.Background())
	if err != nil || state.Status != BackfillComplete || state.LastWatermark == nil {
		t.Fatalf("backfill state = %+v, %v", state, err)
	}
	wantWatermark := filepath.ToSlash(filepath.Join(rollout.SessionsSubdir, "2026", "07", "30", filepath.Base(compressed)))
	if *state.LastWatermark != wantWatermark {
		t.Fatalf("watermark = %q, want %q", *state.LastWatermark, wantWatermark)
	}

	stats, claimed, err = RunRolloutBackfill(context.Background(), runtime, home, RolloutBackfillOptions{})
	if err != nil || claimed || stats != (BackfillStats{}) {
		t.Fatalf("completed rerun = %+v, claimed=%v, err=%v", stats, claimed, err)
	}
}

func TestRolloutBackfillGateWaitsForLeaseAndCountsBadFiles(t *testing.T) {
	t.Run("lease timeout", func(t *testing.T) {
		home := t.TempDir()
		runtime := newBackfillTestRuntimeAt(t, home)
		claimed, err := runtime.TryClaimBackfill(context.Background(), 3600)
		if err != nil || !claimed {
			t.Fatalf("claim = %v, %v", claimed, err)
		}
		err = WaitForRolloutBackfill(context.Background(), runtime, home, RolloutBackfillOptions{
			LeaseSeconds: 3600,
			WaitTimeout:  40 * time.Millisecond,
			PollInterval: 5 * time.Millisecond,
		})
		if err == nil || !strings.Contains(err.Error(), "timed out waiting for state db backfill") {
			t.Fatalf("gate error = %v", err)
		}
	})

	t.Run("bad rollout is accounted and checkpointed", func(t *testing.T) {
		home := t.TempDir()
		runtime := newBackfillTestRuntimeAt(t, home)
		badPath := filepath.Join(home, rollout.SessionsSubdir, "2026", "07", "31", "rollout-2026-07-31T01-02-03-bad.jsonl")
		if err := os.MkdirAll(filepath.Dir(badPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(badPath, []byte("{not-json}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		stats, claimed, err := RunRolloutBackfill(context.Background(), runtime, home, RolloutBackfillOptions{})
		if err != nil || !claimed || stats != (BackfillStats{Scanned: 1, Failed: 1}) {
			t.Fatalf("bad rollout result = %+v, claimed=%v, err=%v", stats, claimed, err)
		}
		state, err := runtime.GetBackfillState(context.Background())
		if err != nil || state.Status != BackfillComplete || state.LastWatermark == nil || *state.LastWatermark != "sessions/2026/07/31/rollout-2026-07-31T01-02-03-bad.jsonl" {
			t.Fatalf("bad rollout state = %+v, %v", state, err)
		}
	})
}

func newBackfillTestRuntime(t *testing.T) *StateRuntime {
	t.Helper()
	return newBackfillTestRuntimeAt(t, t.TempDir())
}

func newBackfillTestRuntimeAt(t *testing.T, home string) *StateRuntime {
	t.Helper()
	config, err := NewSqliteConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := InitStateRuntime(context.Background(), config, "default-provider")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	return runtime
}

func writeBackfillRollout(t *testing.T, home, threadID string, now time.Time, archived bool) string {
	t.Helper()
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome: home, ThreadID: threadID, Source: "cli", ThreadSource: "user",
		CWD: "/workspace", ModelProvider: "openai", HistoryMode: "paginated",
		MemoryMode: "disabled", CLIVersion: "1.2.3", Now: now,
		Git: map[string]string{"sha": "abc123", "branch": "main", "origin_url": "https://example.invalid/repo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := strings.TrimSuffix(threadID, "-thread") + " request"
	payload, _ := json.Marshal(map[string]any{"type": "user_message", "message": request})
	if err := recorder.AppendLine(rollout.Line{Type: "event_msg", Timestamp: now.Add(time.Second).Format(time.RFC3339Nano), Payload: payload}); err != nil {
		t.Fatal(err)
	}
	tokenPayload, _ := json.Marshal(map[string]any{"type": "token_count", "info": map[string]any{"total_token_usage": map[string]any{"total_tokens": 42}}})
	if err := recorder.AppendLine(rollout.Line{Type: "event_msg", Timestamp: now.Add(2 * time.Second).Format(time.RFC3339Nano), Payload: tokenPayload}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	path := recorder.Path()
	if archived {
		path, err = rollout.Archive(path, home)
		if err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func compressBackfillRollout(t *testing.T, plain string) string {
	t.Helper()
	data, err := os.ReadFile(plain)
	if err != nil {
		t.Fatal(err)
	}
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer encoder.Close()
	compressed := plain + ".zst"
	if err := os.WriteFile(compressed, encoder.EncodeAll(data, nil), 0o600); err != nil {
		t.Fatal(err)
	}
	return compressed
}

func assertBackfilledThread(t *testing.T, runtime *StateRuntime, id, sourcePath string, archived bool, preview string) {
	t.Helper()
	var path, source, historyMode, provider, cwd, cliVersion, title, gotPreview, sandboxPolicy, approvalMode, memoryMode string
	var archivedValue bool
	var tokens int64
	err := runtime.StateDB().QueryRow(`
SELECT rollout_path, source, history_mode, model_provider, cwd, cli_version,
       title, preview, sandbox_policy, approval_mode, memory_mode, archived, tokens_used
FROM threads WHERE id = ?`, id).Scan(
		&path, &source, &historyMode, &provider, &cwd, &cliVersion,
		&title, &gotPreview, &sandboxPolicy, &approvalMode, &memoryMode, &archivedValue, &tokens,
	)
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Clean(rollout.PlainRolloutPath(sourcePath))
	if path != wantPath || source != "cli" || historyMode != "paginated" || provider != "openai" || cwd != "/workspace" || cliVersion != "1.2.3" {
		t.Fatalf("thread identity metadata = path:%q source:%q history:%q provider:%q cwd:%q cli:%q", path, source, historyMode, provider, cwd, cliVersion)
	}
	if title != preview || gotPreview != preview || sandboxPolicy != "read-only" || approvalMode != "on-request" || memoryMode != "disabled" || archivedValue != archived || tokens != 42 {
		t.Fatalf("thread derived metadata = title:%q preview:%q sandbox:%q approval:%q memory:%q archived:%v tokens:%d", title, gotPreview, sandboxPolicy, approvalMode, memoryMode, archivedValue, tokens)
	}
}
