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

func TestMigrateRolloutsApplyNotImplementedFailsCleanly(t *testing.T) {
	home := t.TempDir()
	writeTestRollout(t, filepath.Join(home, SessionsSubdir, "rollout-2025-01-01T00-00-00-123e4567-e89b-42d3-a456-426614174020.jsonl"), "123e4567-e89b-42d3-a456-426614174020", "")
	report, err := MigrateRollouts(home, MigrationOptions{Apply: true})
	if err != nil {
		t.Fatalf("MigrateRollouts: %v", err)
	}
	if len(report.Outcomes) != 1 || report.Outcomes[0].Status != MigrationStatusFailed {
		t.Fatalf("apply outcomes = %#v, want one failed (not implemented)", report.Outcomes)
	}
	if report.Outcomes[0].Message == nil || !strings.Contains(*report.Outcomes[0].Message, "not implemented") {
		t.Fatalf("apply message = %#v, want not-implemented note", report.Outcomes[0].Message)
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
