package rollout

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Mirrors Rust's per-user-turn `TurnContextItem` persistence: the record carries
// the turn's model, compaction compatibility hash and cyber access program, and
// a reload recovers them for the previous-turn settings the compaction decision
// reads (#46324, #48224).
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
		TurnID:             "turn-1",
		CWD:                "/work",
		ApprovalPolicy:     "never",
		SandboxPolicy:      "read-only",
		Effort:             "high",
		Personality:        "friendly",
		Model:              "gpt-5.4",
		CompHash:           "hash-a",
		CyberAccessProgram: "daybreak_blue",
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
	model, compHash, program, ok := TurnContextSettings(record.Metadata.TurnContext)
	if !ok || model != "gpt-5.4" || compHash != "hash-a" || program != "daybreak_blue" {
		t.Fatalf("TurnContextSettings() = %q, %q, %q, %v; want gpt-5.4, hash-a, daybreak_blue, true", model, compHash, program, ok)
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
	if _, _, _, ok := TurnContextSettings(nil); ok {
		t.Fatal("nil payload reported settings")
	}
	payload, err := json.Marshal(TurnContextRecord{TurnID: "turn-1", CompHash: "hash-a"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok := TurnContextSettings(payload); ok {
		t.Fatal("model-less payload reported settings")
	}
}

// The program is optional: a turn that selected none persists no field, and a
// reload reports an absent program rather than inheriting another turn's
// (Rust #48224).
func TestTurnContextSettingsKeepsAnAbsentAccessProgramAbsent(t *testing.T) {
	payload, err := json.Marshal(TurnContextRecord{TurnID: "turn-1", Model: "gpt-5.4", CompHash: "hash-a"})
	if err != nil {
		t.Fatal(err)
	}
	model, compHash, program, ok := TurnContextSettings(payload)
	if !ok || model != "gpt-5.4" || compHash != "hash-a" || program != "" {
		t.Fatalf("TurnContextSettings() = %q, %q, %q, %v; want an absent program", model, compHash, program, ok)
	}
	encoded, err := json.Marshal(TurnContextRecord{TurnID: "turn-1", Model: "gpt-5.4"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "cyber_access_program") {
		t.Fatalf("an absent program was serialized: %s", encoded)
	}
}
