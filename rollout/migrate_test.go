package rollout

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
