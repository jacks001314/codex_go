package state

import (
	"context"
	"database/sql"
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

func TestUpsertRolloutThreadPreservesNameOnPaginatedPromotionLikeRust(t *testing.T) {
	home := t.TempDir()
	runtime := newBackfillTestRuntimeAt(t, home)
	ctx := context.Background()
	now := time.Now().UTC()

	metadata := rolloutThreadMetadata{
		id: "promote-thread", path: filepath.Join(home, "sessions", "promote.jsonl"),
		source: "cli", historyMode: "paginated", modelProvider: "openai",
		cwd: "/workspace", cliVersion: "1.2.3", title: "Renamed Thread",
		preview: "preview text", sandboxPolicy: "read-only", approvalMode: "on-request",
		memoryMode: "disabled", createdAt: now, updatedAt: now, recencyAt: now,
		tokensUsed: 1,
	}
	if err := runtime.upsertRolloutThread(ctx, metadata); err != nil {
		t.Fatalf("upsertRolloutThread() error = %v", err)
	}
	var name string
	if err := runtime.StateDB().QueryRow(`SELECT name FROM threads WHERE id = ?`, metadata.id).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "Renamed Thread" {
		t.Fatalf("promoted thread name = %q, want legacy title", name)
	}

	// An existing name must be preserved across a rerun (repair path).
	if _, err := runtime.StateDB().Exec(`UPDATE threads SET name = 'Explicit' WHERE id = ?`, metadata.id); err != nil {
		t.Fatal(err)
	}
	metadata.title = "Changed Title"
	if err := runtime.upsertRolloutThread(ctx, metadata); err != nil {
		t.Fatalf("upsertRolloutThread(rerun) error = %v", err)
	}
	if err := runtime.StateDB().QueryRow(`SELECT name FROM threads WHERE id = ?`, metadata.id).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "Explicit" {
		t.Fatalf("existing thread name overwritten = %q, want Explicit", name)
	}
}

// Rust c0d26949be (#48983, `codex-rs/thread-store/src/local/timestamp_metadata_tests.rs`):
// a rebuild that only advances the thread timestamp writes the timestamp columns
// alone, while a metadata difference and a missing row keep the full repair path.
func TestRolloutRebuildTimestampOnlyTouchesTimestampsLikeRust(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	runtime := newBackfillTestRuntimeAt(t, home)
	threadID := "0199aaaa-2222-7000-8000-000000000d01"
	now := time.Date(2026, 10, 7, 11, 0, 0, 0, time.UTC)
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome: home, ThreadID: threadID, Source: "cli", ThreadSource: "user",
		CWD: "/timestamp-workspace", ModelProvider: "openai", HistoryMode: "paginated",
		MemoryMode: "enabled", CLIVersion: "9.9.9", Now: now,
		Git: map[string]string{"sha": "cafebabe", "branch": "main", "origin_url": "https://example.invalid/timestamps"},
	})
	if err != nil {
		t.Fatal(err)
	}
	userPayload, _ := json.Marshal(map[string]any{"type": "user_message", "message": "timestamp request"})
	if err := recorder.AppendLine(rollout.Line{Type: "event_msg", Timestamp: now.Add(time.Second).Format(time.RFC3339Nano), Payload: userPayload}); err != nil {
		t.Fatal(err)
	}
	tokenPayload, _ := json.Marshal(map[string]any{"type": "token_count", "info": map[string]any{"total_token_usage": map[string]any{"total_tokens": 7}}})
	if err := recorder.AppendLine(rollout.Line{Type: "event_msg", Timestamp: now.Add(2 * time.Second).Format(time.RFC3339Nano), Payload: tokenPayload}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	path := recorder.Path()
	if err := runtime.ReconcileRollout(ctx, path, false); err != nil {
		t.Fatalf("initial ReconcileRollout() error = %v", err)
	}
	before := readTimestampProbeRow(t, runtime, threadID)
	if before.tokensUsed != 7 {
		t.Fatalf("fixture tokens_used = %d, want 7", before.tokensUsed)
	}
	for _, stmt := range []string{
		`CREATE TABLE timestamp_writes(kind TEXT)`,
		`CREATE TRIGGER count_timestamp AFTER UPDATE OF updated_at_ms ON threads BEGIN INSERT INTO timestamp_writes VALUES ('timestamp'); END`,
		`CREATE TRIGGER count_full_row AFTER UPDATE OF title ON threads BEGIN INSERT INTO timestamp_writes VALUES ('full'); END`,
	} {
		if _, err := runtime.StateDB().ExecContext(ctx, stmt); err != nil {
			t.Fatalf("install write counter: %v", err)
		}
	}

	// Timestamp-only observation: only the rollout mtime moved.
	future := now.Add(3 * time.Hour)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	if err := runtime.ReconcileRollout(ctx, path, false); err != nil {
		t.Fatalf("timestamp-only ReconcileRollout() error = %v", err)
	}
	timestamps, fullRows := readTimestampProbeWrites(t, runtime)
	if timestamps != 1 || fullRows != 0 {
		t.Fatalf("timestamp-only rebuild writes = timestamp:%d full:%d, want timestamp:1 full:0", timestamps, fullRows)
	}
	after := readTimestampProbeRow(t, runtime, threadID)
	if after != before {
		t.Fatalf("timestamp-only rebuild changed metadata: %+v, want %+v", after, before)
	}

	// Any metadata difference keeps the full repair path.
	metadata, err := extractRolloutThreadMetadata(path, false, "default-provider")
	if err != nil {
		t.Fatalf("extractRolloutThreadMetadata() error = %v", err)
	}
	metadata.tokensUsed = 99
	if err := runtime.upsertRolloutThread(ctx, metadata); err != nil {
		t.Fatalf("upsertRolloutThread() error = %v", err)
	}
	if _, fullRows = readTimestampProbeWrites(t, runtime); fullRows != 1 {
		t.Fatalf("metadata rebuild full-row writes = %d, want 1", fullRows)
	}
	if changed := readTimestampProbeRow(t, runtime, threadID); changed.tokensUsed != 99 {
		t.Fatalf("tokens_used after metadata change = %d, want 99", changed.tokensUsed)
	}

	// Missing rows still need the full repair path.
	if _, err := runtime.StateDB().ExecContext(ctx, `DELETE FROM threads WHERE id = ?`, threadID); err != nil {
		t.Fatal(err)
	}
	if err := runtime.ReconcileRollout(ctx, path, false); err != nil {
		t.Fatalf("missing-row ReconcileRollout() error = %v", err)
	}
	repaired := readTimestampProbeRow(t, runtime, threadID)
	if repaired.tokensUsed != 7 || repaired.rolloutPath != filepath.Clean(path) || repaired.gitSHA != "cafebabe" || repaired.cwd != "/timestamp-workspace" {
		t.Fatalf("repaired row = %+v", repaired)
	}
}

type timestampProbeRow struct {
	title, model, cwd, preview, firstUserMessage string
	gitSHA, gitBranch, rolloutPath               string
	tokensUsed                                   int64
}

func readTimestampProbeRow(t *testing.T, runtime *StateRuntime, threadID string) timestampProbeRow {
	t.Helper()
	var row timestampProbeRow
	var title, model, cwd, preview, firstUserMessage, gitSHA, gitBranch, rolloutPath sql.NullString
	var tokensUsed sql.NullInt64
	if err := runtime.StateDB().QueryRowContext(context.Background(),
		`SELECT title, model, cwd, preview, first_user_message, git_sha, git_branch, tokens_used, rollout_path FROM threads WHERE id = ?`,
		threadID).Scan(&title, &model, &cwd, &preview, &firstUserMessage, &gitSHA, &gitBranch, &tokensUsed, &rolloutPath); err != nil {
		t.Fatalf("read stored thread row: %v", err)
	}
	row.title, row.model, row.cwd, row.preview = title.String, model.String, cwd.String, preview.String
	row.firstUserMessage, row.gitSHA, row.gitBranch = firstUserMessage.String, gitSHA.String, gitBranch.String
	row.tokensUsed, row.rolloutPath = tokensUsed.Int64, rolloutPath.String
	return row
}

func readTimestampProbeWrites(t *testing.T, runtime *StateRuntime) (timestamps int, fullRows int) {
	t.Helper()
	rows, err := runtime.StateDB().QueryContext(context.Background(), `SELECT kind, COUNT(*) FROM timestamp_writes GROUP BY kind`)
	if err != nil {
		t.Fatalf("read write counters: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		var count int
		if err := rows.Scan(&kind, &count); err != nil {
			t.Fatalf("scan write counters: %v", err)
		}
		switch kind {
		case "timestamp":
			timestamps = count
		case "full":
			fullRows = count
		}
	}
	return timestamps, fullRows
}
