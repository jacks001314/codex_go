package appserver

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/session"
)

const planCollaborationModeJSON = `{"mode":"plan","settings":{"model":"gpt-5-plan","reasoning_effort":"high","developer_instructions":"plan first"}}`

// TestThreadResumeReportsLiveCollaborationMode mirrors Rust #45519: a resume
// reports the effective collaboration mode so reconnecting clients can
// reconcile a mode changed by another client.
func TestThreadResumeReportsLiveCollaborationMode(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(session.NewStore(t.TempDir()))})
	if _, err := router.requireThreadExtras().UpdateSettings(&SettingsUpdateParams{
		ThreadID:          "thread-plan",
		CollaborationMode: mustDecodeAnyMap(t, planCollaborationModeJSON),
	}); err != nil {
		t.Fatalf("UpdateSettings() error = %v", err)
	}
	response := &ThreadResumeResponse{Thread: &Thread{ID: "thread-plan"}}
	router.applyThreadResumeCollaborationMode(response)
	if response.CollaborationMode == nil {
		t.Fatal("collaborationMode = nil, want the live plan mode")
	}
	if response.CollaborationMode.Mode != ModeKindPlan ||
		response.CollaborationMode.Settings.Model != "gpt-5-plan" ||
		response.CollaborationMode.Settings.ReasoningEffort == nil ||
		*response.CollaborationMode.Settings.ReasoningEffort != ReasoningEffort("high") {
		t.Fatalf("collaborationMode = %#v", response.CollaborationMode)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("Marshal(response) error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal(response) error = %v", err)
	}
	mode, ok := decoded["collaborationMode"].(map[string]any)
	if !ok || strings.TrimSpace(stringFromAnyMapValue(mode, "mode")) != "plan" {
		t.Fatalf("serialized collaborationMode = %#v", decoded["collaborationMode"])
	}
	if _, present := decoded["collaborationMode"]; !present {
		t.Fatal("serialized response omitted collaborationMode")
	}
}

// TestThreadResumeRestoresPersistedCollaborationMode mirrors the restore half of
// Rust #45519: a cold resume restores the saved Plan mode for the resumed
// session (so its first turn keeps using it) rather than falling back to
// Default.
func TestThreadResumeRestoresPersistedCollaborationMode(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(session.NewStore(t.TempDir()))})
	response := &ThreadResumeResponse{
		Thread:            &Thread{ID: "thread-plan"},
		CollaborationMode: collaborationModeFromRaw(json.RawMessage(planCollaborationModeJSON)),
	}
	router.applyThreadResumeCollaborationMode(response)

	restored := router.threadSettingsForTurn("thread-plan")
	if restored == nil || restored.CollaborationMode == nil {
		t.Fatalf("restored settings = %#v", restored)
	}
	if strings.TrimSpace(stringFromAnyMapValue(restored.CollaborationMode, "mode")) != "plan" {
		t.Fatalf("restored collaboration mode = %#v", restored.CollaborationMode)
	}
}

// TestLatestPersistedCollaborationModeReadsWorldState covers Go's compact
// world-state source, which the resume response falls back to when the rollout
// carries no settings snapshot.
func TestLatestPersistedCollaborationModeReadsWorldState(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(filepath.Join(home, "sessions"))
	worldState, err := session.EncodeWorldState(&session.WorldState{
		CollaborationMode: json.RawMessage(planCollaborationModeJSON),
	})
	if err != nil {
		t.Fatalf("EncodeWorldState() error = %v", err)
	}
	record := &session.Record{
		ID:        "thread-plan",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		Metadata:  session.Metadata{WorldState: worldState},
	}
	if err := store.Create(record); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}
	router := NewRouter(store)
	mode := router.latestPersistedCollaborationMode("thread-plan", record)
	if mode == nil || mode.Mode != ModeKindPlan {
		t.Fatalf("latestPersistedCollaborationMode() = %#v, want plan", mode)
	}
	if mode.Settings.DeveloperInstructions == nil || *mode.Settings.DeveloperInstructions != "plan first" {
		t.Fatalf("restored developer instructions = %#v", mode.Settings.DeveloperInstructions)
	}
}

func mustDecodeAnyMap(t *testing.T, raw string) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("Unmarshal(%s) error = %v", raw, err)
	}
	return decoded
}

func stringFromAnyMapValue(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, ok := values[key].(string)
	if !ok {
		return ""
	}
	return value
}
