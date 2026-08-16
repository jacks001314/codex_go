package rollout

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

func writeTestRollout(t *testing.T, path string, threadID string, historyMode string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	meta := map[string]any{
		"id":          threadID,
		"session_id":  "session-test",
		"timestamp":   now,
		"cwd":         "/workspace",
		"originator":  "test",
		"model":       "gpt-test",
		"cli_version": "0.0.0-test",
	}
	if historyMode != "" {
		meta["history_mode"] = historyMode
	}
	data, err := jsonMarshal(meta)
	if err != nil {
		t.Fatalf("marshal meta: %v", err)
	}
	line := map[string]any{
		"type":      "session_meta",
		"timestamp": now,
		"payload":   json.RawMessage(data),
	}
	encoded, err := json.Marshal(line)
	if err != nil {
		t.Fatalf("marshal line: %v", err)
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestCanonicalizeRolloutMigratesCompressedRolloutLikeRust(t *testing.T) {
	// Mirrors Rust migration_preserves_compressed_rollouts_during_publish: a
	// .jsonl.zst source stays compressed after migration; the published file
	// loads as paginated and replays its content.
	home := t.TempDir()
	threadID := "123e4567-e89b-42d3-a456-426614174023"
	plainPath := filepath.Join(home, SessionsSubdir, "rollout-2025-01-01T00-00-00-"+threadID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(plainPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	now := "2025-01-01T00:00:00Z"
	meta := map[string]any{
		"id": threadID, "session_id": "s", "timestamp": now, "cwd": "/w",
		"originator": "o", "model": "m", "cli_version": "v",
	}
	data, _ := json.Marshal(meta)
	lines := []string{
		string(mustJSON(t, map[string]any{"type": "session_meta", "timestamp": now, "payload": json.RawMessage(data)})),
		string(mustJSON(t, map[string]any{"type": "event_msg", "timestamp": now, "payload": map[string]any{"type": "task_started", "turn_id": "turn-1", "started_at": 1700000000}})),
		string(mustJSON(t, map[string]any{"type": "event_msg", "timestamp": now, "payload": map[string]any{"type": "user_message", "client_id": "c1", "message": "compressed hello"}})),
		string(mustJSON(t, map[string]any{"type": "event_msg", "timestamp": now, "payload": map[string]any{"type": "agent_message", "message": "compressed answer"}})),
		"",
	}
	plain := []byte(strings.Join(lines, "\n"))
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatalf("zstd.NewWriter: %v", err)
	}
	compressed := encoder.EncodeAll(plain, nil)
	compressedPath := plainPath + ".zst"
	if err := os.WriteFile(compressedPath, compressed, 0o600); err != nil {
		t.Fatalf("WriteFile compressed: %v", err)
	}

	if err := CanonicalizeRollout(home, compressedPath); err != nil {
		t.Fatalf("CanonicalizeRollout(compressed): %v", err)
	}
	// The source is still compressed and no plain or staged files remain.
	if _, err := os.Stat(compressedPath); err != nil {
		t.Fatalf("compressed source missing after publish: %v", err)
	}
	if _, err := os.Stat(plainPath); !os.IsNotExist(err) {
		t.Fatalf("plain sibling created unexpectedly (err=%v)", err)
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(compressedPath), ".*.tmp"))
	if err != nil {
		t.Fatalf("Glob staged: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("staged temp files remain: %v", matches)
	}

	lines2, _, err := Load(compressedPath)
	if err != nil {
		t.Fatalf("Load migrated compressed: %v", err)
	}
	if lines2[0].Meta == nil || !strings.EqualFold(strings.TrimSpace(lines2[0].Meta.HistoryMode), "paginated") {
		t.Fatalf("migrated compressed head meta = %#v, want paginated", lines2[0].Meta)
	}
	rec, err := RecordFromPath(compressedPath, false)
	if err != nil {
		t.Fatalf("RecordFromPath compressed: %v", err)
	}
	if rec.Preview != "compressed hello" {
		t.Fatalf("preview = %q, want compressed hello", rec.Preview)
	}
}

func TestCanonicalizeRolloutSetsSubagentHistoryBoundaryLikeRust(t *testing.T) {
	// Mirrors Rust rewrite_subagent_history_boundary: a migrated subagent
	// rollout marks the ordinal from which its own history starts; ordinary
	// rollouts leave subagent_history_start_ordinal unset.
	home := t.TempDir()
	subagentID := "123e4567-e89b-42d3-a456-426614174024"
	subagentPath := filepath.Join(home, SessionsSubdir, "rollout-2025-01-01T00-00-00-"+subagentID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(subagentPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	now := "2025-01-01T00:00:00Z"
	meta := map[string]any{
		"id": subagentID, "session_id": "s", "timestamp": now, "cwd": "/w",
		"originator": "o", "model": "m", "cli_version": "v",
		"source": "subagent:thread_spawn", "thread_source": "subAgentThreadSpawn",
	}
	data, _ := json.Marshal(meta)
	lines := []string{
		string(mustJSON(t, map[string]any{"type": "session_meta", "timestamp": now, "payload": json.RawMessage(data)})),
		string(mustJSON(t, map[string]any{"type": "event_msg", "timestamp": now, "payload": map[string]any{"type": "task_started", "turn_id": "turn-1", "started_at": 1700000000}})),
		string(mustJSON(t, map[string]any{"type": "event_msg", "timestamp": now, "payload": map[string]any{"type": "user_message", "client_id": "c1", "message": "subagent hello"}})),
		string(mustJSON(t, map[string]any{"type": "event_msg", "timestamp": now, "payload": map[string]any{"type": "agent_message", "message": "subagent answer"}})),
		"",
	}
	if err := os.WriteFile(subagentPath, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := CanonicalizeRollout(home, subagentPath); err != nil {
		t.Fatalf("CanonicalizeRollout(subagent): %v", err)
	}
	metaOut, err := FirstSessionMeta(subagentPath)
	if err != nil {
		t.Fatalf("FirstSessionMeta: %v", err)
	}
	if metaOut.SubagentHistoryStartOrdinal == nil {
		t.Fatal("subagent_history_start_ordinal unset after migration; want the first own-history ordinal")
	}
	if *metaOut.SubagentHistoryStartOrdinal != 4 {
		t.Fatalf("subagent_history_start_ordinal = %d, want 4 (meta+started+2 items)", *metaOut.SubagentHistoryStartOrdinal)
	}

	// Ordinary rollout keeps subagent_history_start_ordinal unset.
	ordinaryID := "123e4567-e89b-42d3-a456-426614174025"
	ordinaryPath := filepath.Join(home, SessionsSubdir, "rollout-2025-01-01T00-00-00-"+ordinaryID+".jsonl")
	writeTestRollout(t, ordinaryPath, ordinaryID, "")
	if err := CanonicalizeRollout(home, ordinaryPath); err != nil {
		t.Fatalf("CanonicalizeRollout(ordinary): %v", err)
	}
	ordinaryMeta, err := FirstSessionMeta(ordinaryPath)
	if err != nil {
		t.Fatalf("FirstSessionMeta ordinary: %v", err)
	}
	if ordinaryMeta.SubagentHistoryStartOrdinal != nil {
		t.Fatalf("ordinary rollout subagent_history_start_ordinal = %d, want nil", *ordinaryMeta.SubagentHistoryStartOrdinal)
	}
}

func TestCanonicalizeRolloutReplaysRollbackMarkerLikeRust(t *testing.T) {
	// Mirrors Rust rollout_migration_tests.rs: legacy rollbacks remove logical
	// instruction turns; a ThreadRolledBack marker drops the rolled-back turn's
	// records from the canonical output.
	home := t.TempDir()
	threadID := "123e4567-e89b-42d3-a456-426614174022"
	path := filepath.Join(home, SessionsSubdir, "rollout-2025-01-01T00-00-00-"+threadID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	now := "2025-01-01T00:00:00Z"
	meta := map[string]any{
		"id": threadID, "session_id": "s", "timestamp": now, "cwd": "/w",
		"originator": "o", "model": "m", "cli_version": "v",
	}
	data, _ := json.Marshal(meta)
	lines := []string{
		string(mustJSON(t, map[string]any{"type": "session_meta", "timestamp": now, "payload": json.RawMessage(data)})),
		string(mustJSON(t, map[string]any{"type": "event_msg", "timestamp": now, "payload": map[string]any{"type": "task_started", "turn_id": "turn-1", "started_at": 1700000000}})),
		string(mustJSON(t, map[string]any{"type": "event_msg", "timestamp": now, "payload": map[string]any{"type": "user_message", "client_id": "c1", "message": "first"}})),
		string(mustJSON(t, map[string]any{"type": "event_msg", "timestamp": now, "payload": map[string]any{"type": "agent_message", "message": "answer-1"}})),
		string(mustJSON(t, map[string]any{"type": "event_msg", "timestamp": now, "payload": map[string]any{"type": "task_complete", "turn_id": "turn-1", "completed_at": 1700000005}})),
		string(mustJSON(t, map[string]any{"type": "event_msg", "timestamp": now, "payload": map[string]any{"type": "task_started", "turn_id": "turn-2", "started_at": 1700000010}})),
		string(mustJSON(t, map[string]any{"type": "event_msg", "timestamp": now, "payload": map[string]any{"type": "user_message", "client_id": "c1", "message": "rolled back"}})),
		string(mustJSON(t, map[string]any{"type": "event_msg", "timestamp": now, "payload": map[string]any{"type": "thread_rolled_back", "num_turns": 1}})),
		"",
	}
	out := strings.Join(lines, "\n")
	if err := os.WriteFile(path, []byte(out), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := CanonicalizeRollout(home, path); err != nil {
		t.Fatalf("CanonicalizeRollout: %v", err)
	}
	rec, err := RecordFromPath(path, false)
	if err != nil {
		t.Fatalf("RecordFromPath: %v", err)
	}
	var texts []string
	for i := range rec.Items {
		texts = append(texts, rec.Items[i].Text)
	}
	joined := strings.Join(texts, "|")
	if strings.Contains(joined, "rolled back") {
		t.Fatalf("rolled-back turn survived canonicalization: %v", texts)
	}
	if !strings.Contains(joined, "first") || !strings.Contains(joined, "answer-1") {
		t.Fatalf("surviving turn lost in canonicalization: %v", texts)
	}
}

func jsonMarshal(value any) ([]byte, error) {
	return json.Marshal(value)
}

// writeTestRolloutWithEvents writes a legacy rollout with a turn boundary and
// user/agent messages so canonicalization has content to replay.
func writeTestRolloutWithEvents(t *testing.T, path string, threadID string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	now := "2025-01-01T00:00:00Z"
	meta := map[string]any{
		"id":          threadID,
		"session_id":  "session-test",
		"timestamp":   now,
		"cwd":         "/workspace",
		"originator":  "test",
		"model":       "gpt-test",
		"cli_version": "0.0.0-test",
	}
	data, _ := json.Marshal(meta)
	lines := []string{
		string(mustJSON(t, map[string]any{"type": "session_meta", "timestamp": now, "payload": json.RawMessage(data)})),
		string(mustJSON(t, map[string]any{"type": "event_msg", "timestamp": now, "payload": map[string]any{"type": "task_started", "turn_id": "turn-1", "started_at": 1700000000}})),
		string(mustJSON(t, map[string]any{"type": "event_msg", "timestamp": now, "payload": map[string]any{"type": "user_message", "client_id": "client-1", "message": "hello event"}})),
		string(mustJSON(t, map[string]any{"type": "event_msg", "timestamp": now, "payload": map[string]any{"type": "agent_message", "message": "answer event"}})),
		string(mustJSON(t, map[string]any{"type": "event_msg", "timestamp": now, "payload": map[string]any{"type": "task_complete", "turn_id": "turn-1", "completed_at": 1700000005, "duration_ms": 5000}})),
		"",
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return data
}

func TestMigrateRolloutsDryRunClassifiesLikeRust(t *testing.T) {
	// Mirrors Rust rollout_migration_tests.rs dry-run inspection: legacy
	// rollouts (active, archived, compressed naming) are Eligible, paginated
	// ones are AlreadyPaginated, empty files are SkippedEmpty.
	home := t.TempDir()
	rootID := "123e4567-e89b-42d3-a456-426614174000"
	archivedID := "123e4567-e89b-42d3-a456-426614174001"
	paginatedID := "123e4567-e89b-42d3-a456-426614174002"
	emptyID := "123e4567-e89b-42d3-a456-426614174003"

	writeTestRollout(t, filepath.Join(home, SessionsSubdir, "rollout-2025-01-01T00-00-00-"+rootID+".jsonl"), rootID, "")
	writeTestRollout(t, filepath.Join(home, ArchivedSessionsSubdir, "rollout-2025-01-01T00-00-00-"+archivedID+".jsonl"), archivedID, "")
	writeTestRollout(t, filepath.Join(home, SessionsSubdir, "rollout-2025-01-01T00-00-00-"+paginatedID+".jsonl"), paginatedID, "paginated")
	emptyPath := filepath.Join(home, SessionsSubdir, "rollout-2025-01-01T00-00-00-"+emptyID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(emptyPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(emptyPath, nil, 0o600); err != nil {
		t.Fatalf("WriteFile empty: %v", err)
	}

	report, err := MigrateRollouts(home, MigrationOptions{})
	if err != nil {
		t.Fatalf("MigrateRollouts: %v", err)
	}
	if len(report.Outcomes) != 4 {
		t.Fatalf("outcomes = %d, want 4: %#v", len(report.Outcomes), report.Outcomes)
	}
	byID := map[string]MigrationStatus{}
	for _, outcome := range report.Outcomes {
		if outcome.ThreadID != nil {
			byID[*outcome.ThreadID] = outcome.Status
		}
	}
	if byID[rootID] != MigrationStatusEligible {
		t.Fatalf("root status = %q, want eligible", byID[rootID])
	}
	if byID[archivedID] != MigrationStatusEligible {
		t.Fatalf("archived status = %q, want eligible", byID[archivedID])
	}
	if byID[paginatedID] != MigrationStatusAlreadyPaginated {
		t.Fatalf("paginated status = %q, want already_paginated", byID[paginatedID])
	}
	if byID[emptyID] != MigrationStatusSkippedEmpty {
		t.Fatalf("empty status = %q, want skipped_empty", byID[emptyID])
	}
}

func TestMigrateRolloutsThreadSelection(t *testing.T) {
	home := t.TempDir()
	keepID := "123e4567-e89b-42d3-a456-426614174010"
	skipID := "123e4567-e89b-42d3-a456-426614174011"
	writeTestRollout(t, filepath.Join(home, SessionsSubdir, "rollout-2025-01-01T00-00-00-"+keepID+".jsonl"), keepID, "")
	writeTestRollout(t, filepath.Join(home, SessionsSubdir, "rollout-2025-01-01T00-00-00-"+skipID+".jsonl"), skipID, "")

	report, err := MigrateRollouts(home, MigrationOptions{ThreadIDs: []string{keepID}})
	if err != nil {
		t.Fatalf("MigrateRollouts: %v", err)
	}
	if len(report.Outcomes) != 1 || report.Outcomes[0].ThreadID == nil || *report.Outcomes[0].ThreadID != keepID {
		t.Fatalf("selected outcomes = %#v, want only %q", report.Outcomes, keepID)
	}
}

func TestMigrateRolloutsApplyMigratesEligibleRollout(t *testing.T) {
	home := t.TempDir()
	threadID := "123e4567-e89b-42d3-a456-426614174020"
	path := filepath.Join(home, SessionsSubdir, "rollout-2025-01-01T00-00-00-"+threadID+".jsonl")
	writeTestRollout(t, path, threadID, "")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	report, err := MigrateRollouts(home, MigrationOptions{Apply: true})
	if err != nil {
		t.Fatalf("MigrateRollouts: %v", err)
	}
	if len(report.Outcomes) != 1 || report.Outcomes[0].Status != MigrationStatusMigrated {
		t.Fatalf("apply outcomes = %#v, want one migrated", report.Outcomes)
	}
	// The source file is now paginated (history_mode=paginated), the staged
	// temp file is gone, and no journal remains.
	meta, err := FirstSessionMeta(path)
	if err != nil {
		t.Fatalf("FirstSessionMeta after migrate: %v", err)
	}
	if !strings.EqualFold(strings.TrimSpace(meta.HistoryMode), "paginated") {
		t.Fatalf("history mode = %q, want paginated", meta.HistoryMode)
	}
	if _, err := os.Stat(path + ".paginated.tmp"); !os.IsNotExist(err) {
		t.Fatalf("staged temp file still present (err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(home, MigrationJournalDirectory, threadID+".pending")); !os.IsNotExist(err) {
		t.Fatalf("migration journal still present (err=%v)", err)
	}
	// Dry-run again reports AlreadyPaginated.
	report, err = MigrateRollouts(home, MigrationOptions{})
	if err != nil {
		t.Fatalf("MigrateRollouts dry-run: %v", err)
	}
	if len(report.Outcomes) != 1 || report.Outcomes[0].Status != MigrationStatusAlreadyPaginated {
		t.Fatalf("post-migrate outcomes = %#v, want already_paginated", report.Outcomes)
	}
	_ = original
}

func TestCanonicalizeRolloutWritesPaginatedStagedAndPublishes(t *testing.T) {
	home := t.TempDir()
	threadID := "123e4567-e89b-42d3-a456-426614174021"
	path := filepath.Join(home, SessionsSubdir, "rollout-2025-01-01T00-00-00-"+threadID+".jsonl")
	writeTestRolloutWithEvents(t, path, threadID)
	if err := CanonicalizeRollout(home, path); err != nil {
		t.Fatalf("CanonicalizeRollout: %v", err)
	}
	lines, _, err := Load(path)
	if err != nil {
		t.Fatalf("Load migrated rollout: %v", err)
	}
	if len(lines) < 4 {
		t.Fatalf("migrated lines = %d, want session meta + turn + items", len(lines))
	}
	if lines[0].Meta == nil || !strings.EqualFold(strings.TrimSpace(lines[0].Meta.HistoryMode), "paginated") {
		t.Fatalf("migrated head meta = %#v, want paginated", lines[0].Meta)
	}
	var eventTypes []string
	for i := 1; i < len(lines); i++ {
		var payload struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(lines[i].Payload, &payload) == nil {
			eventTypes = append(eventTypes, payload.Type)
		}
	}
	joined := strings.Join(eventTypes, ",")
	if !strings.Contains(joined, "task_started") || !strings.Contains(joined, "task_complete") {
		t.Fatalf("migrated event types = %v, want task_started and task_complete", eventTypes)
	}
}

func TestRenderJSONReportMirrorsRustShape(t *testing.T) {
	id := "thread-json"
	report := &MigrationReport{Outcomes: []MigrationOutcome{
		{ThreadID: &id, RolloutPath: "/sessions/rollout.jsonl", Status: MigrationStatusEligible, BytesProcessed: 0},
	}}
	data, err := RenderJSONReport(report)
	if err != nil {
		t.Fatalf("RenderJSONReport: %v", err)
	}
	text := string(data)
	for _, want := range []string{`"thread_id"`, `"rollout_path"`, `"status"`, `"eligible"`, `"bytes_processed"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("JSON missing %q: %s", want, text)
		}
	}
}
