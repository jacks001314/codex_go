package rollout

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestLatestPersistedCollaborationModeRestoresSavedMode mirrors Rust #45519: a
// resume restores the collaboration mode from the latest thread-owned settings
// snapshot, ignoring a fork-copied snapshot owned by another thread, and falls
// back to the legacy TurnContext field.
func TestLatestPersistedCollaborationModeRestoresSavedMode(t *testing.T) {
	home := t.TempDir()
	recorder, err := NewRecorder(&CreateParams{CodexHome: home, ThreadID: "thread-1", CWD: "/w"})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	path := recorder.Path()
	if err := recorder.AppendThreadSettingsAppliedWithOwner("thread-1", "on-request", "/w", time.Now()); err != nil {
		t.Fatalf("AppendThreadSettingsAppliedWithOwner() error = %v", err)
	}
	planMode := json.RawMessage(`{"mode":"plan","settings":{"model":"gpt-5","reasoning_effort":"high","developer_instructions":"plan first"}}`)
	if err := recorder.AppendThreadSettingsAppliedWithSnapshot("thread-1", "on-request", "/w", planMode, time.Now()); err != nil {
		t.Fatalf("AppendThreadSettingsAppliedWithSnapshot() error = %v", err)
	}
	// A fork-copied snapshot owned by another thread must not win.
	foreign := json.RawMessage(`{"mode":"default","settings":{"model":"other","reasoning_effort":null,"developer_instructions":null}}`)
	if err := recorder.AppendThreadSettingsAppliedWithSnapshot("thread-2", "on-request", "/other", foreign, time.Now()); err != nil {
		t.Fatalf("AppendThreadSettingsAppliedWithSnapshot(foreign) error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	lines, _, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	got := LatestPersistedCollaborationMode("thread-1", lines)
	var mode struct {
		Mode     string `json:"mode"`
		Settings struct {
			Model               string  `json:"model"`
			ReasoningEffort     *string `json:"reasoning_effort"`
			DeveloperInstructns *string `json:"developer_instructions"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(got, &mode); err != nil {
		t.Fatalf("Unmarshal(%s) error = %v", got, err)
	}
	if mode.Mode != "plan" || mode.Settings.Model != "gpt-5" ||
		mode.Settings.ReasoningEffort == nil || *mode.Settings.ReasoningEffort != "high" {
		t.Fatalf("restored mode = %s", got)
	}
}

// TestLatestPersistedCollaborationModeFallsBackToTurnContext mirrors Rust's
// legacy fallback: rollouts written before the settings snapshot carried a mode
// still restore the last TurnContext collaboration mode.
func TestLatestPersistedCollaborationModeFallsBackToTurnContext(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "rollout-2025-01-01T00-00-00-thread-1.jsonl")
	lines := []Line{
		{
			Type:        "turn_context",
			TurnContext: json.RawMessage(`{"turn_id":"t1","collaboration_mode":{"mode":"plan","settings":{"model":"m","reasoning_effort":"low","developer_instructions":null}}}`),
		},
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	got := LatestPersistedCollaborationMode("thread-1", lines)
	if len(got) == 0 {
		t.Fatal("LatestPersistedCollaborationMode() = nil, want the TurnContext mode")
	}
	var mode struct {
		Mode string `json:"mode"`
	}
	if err := json.Unmarshal(got, &mode); err != nil {
		t.Fatalf("Unmarshal(%s) error = %v", got, err)
	}
	if mode.Mode != "plan" {
		t.Fatalf("turn context mode = %q, want plan", mode.Mode)
	}
}
