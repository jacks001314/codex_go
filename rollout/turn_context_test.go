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

// Mirrors Rust #51492: the persisted turn context drops the obsolete fields
// (`personality`, `workspace_roots`, `current_date`, `timezone`, `network`,
// `multi_agent_mode`) and records the legacy `summary` placeholder instead of
// the active reasoning-summary setting. Reading stays tolerant of a record that
// omits `summary` entirely, which is how newer Rust readers accept the field's
// future removal.
func TestTurnContextRecordOmitsRemovedFieldsAndWritesSummaryPlaceholder(t *testing.T) {
	payload, err := json.Marshal(TurnContextRecord{
		TurnID:             "turn-1",
		CWD:                "/work",
		ApprovalPolicy:     "never",
		SandboxPolicy:      "read-only",
		Effort:             "high",
		Model:              "gpt-5.4",
		CompHash:           "hash-a",
		CyberAccessProgram: "daybreak_blue",
		Summary:            TurnContextSummaryPlaceholder,
	})
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]any
	if err := json.Unmarshal(payload, &values); err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{"personality", "workspace_roots", "current_date", "timezone", "network", "multi_agent_mode"} {
		if _, ok := values[removed]; ok {
			t.Fatalf("obsolete field %q was persisted: %s", removed, payload)
		}
	}
	if got := values["summary"]; got != "none" {
		t.Fatalf("summary = %#v, want the %q placeholder: %s", got, TurnContextSummaryPlaceholder, payload)
	}

	// A record written without `summary` still resolves its settings.
	legacy, err := json.Marshal(map[string]any{"turn_id": "turn-1", "model": "gpt-5.4", "comp_hash": "hash-a"})
	if err != nil {
		t.Fatal(err)
	}
	model, compHash, program, ok := TurnContextSettings(legacy)
	if !ok || model != "gpt-5.4" || compHash != "hash-a" || program != "" {
		t.Fatalf("TurnContextSettings(legacy) = %q, %q, %q, %v", model, compHash, program, ok)
	}
}
