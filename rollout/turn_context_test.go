package rollout

import (
	"encoding/json"
	"testing"
	"time"
)

// Mirrors Rust's per-user-turn `TurnContextItem` persistence: the record carries
// the turn's model and compaction compatibility hash, and a reload recovers
// them for the previous-turn settings the compaction decision reads (#46324).
func TestAppendTurnContextRecordsPreviousTurnSettingsLikeRust(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	recorder, err := NewRecorder(&CreateParams{
		CodexHome: home,
		ThreadID:  "thread-turn-context",
		Now:       now,
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	path := recorder.Path()
	if err := recorder.AppendTurnContext(TurnContextRecord{
		TurnID:         "turn-1",
		CWD:            "/work",
		ApprovalPolicy: "never",
		SandboxPolicy:  "read-only",
		Effort:         "high",
		Personality:    "friendly",
		Model:          "gpt-5.4",
		CompHash:       "hash-a",
	}, now); err != nil {
		t.Fatalf("AppendTurnContext() error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	record, err := RecordFromPath(path, false)
	if err != nil {
		t.Fatalf("RecordFromPath() error = %v", err)
	}
	model, compHash, ok := TurnContextSettings(record.Metadata.TurnContext)
	if !ok || model != "gpt-5.4" || compHash != "hash-a" {
		t.Fatalf("TurnContextSettings() = %q, %q, %v; want gpt-5.4, hash-a, true", model, compHash, ok)
	}
	// The rollout loader also applies the record to the thread metadata, so a
	// resumed thread resumes with the model its rollout was recorded with.
	if record.Metadata.Model != "gpt-5.4" {
		t.Fatalf("recovered model = %q, want gpt-5.4", record.Metadata.Model)
	}
	if record.Metadata.CWD != "/work" {
		t.Fatalf("recovered cwd = %q, want /work", record.Metadata.CWD)
	}
}

func TestTurnContextSettingsRejectsRecordsWithoutAModel(t *testing.T) {
	if _, _, ok := TurnContextSettings(nil); ok {
		t.Fatal("nil payload reported settings")
	}
	payload, err := json.Marshal(TurnContextRecord{TurnID: "turn-1", CompHash: "hash-a"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := TurnContextSettings(payload); ok {
		t.Fatal("model-less payload reported settings")
	}
}
