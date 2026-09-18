package appserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/rollout"
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
	router.applyThreadResumeCollaborationMode(response, nil)
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
	router.applyThreadResumeCollaborationMode(response, nil)

	restored := router.threadSettingsForTurn("thread-plan")
	if restored == nil || restored.CollaborationMode == nil {
		t.Fatalf("restored settings = %#v", restored)
	}
	if strings.TrimSpace(stringFromAnyMapValue(restored.CollaborationMode, "mode")) != "plan" {
		t.Fatalf("restored collaboration mode = %#v", restored.CollaborationMode)
	}
}

// TestThreadResumeOverlaysEffectiveModelAndEffortLikeRust mirrors Rust #45519's
// `saved_mode.with_updates(Some(model), Some(config.model_reasoning_effort), None)`:
// a cold resume keeps the saved mode and developer instructions, takes the
// resumed session's effective model and reasoning effort, reports the result
// (and the effective effort), and restores that mode for the session.
func TestThreadResumeOverlaysEffectiveModelAndEffortLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(session.NewStore(t.TempDir())),
		Config:       config.NewConfigService(t.TempDir()),
	})
	response := &ThreadResumeResponse{
		Thread:            &Thread{ID: "thread-plan"},
		CollaborationMode: collaborationModeFromRaw(json.RawMessage(planCollaborationModeJSON)),
	}
	cfg := &config.Config{Values: map[string]any{"model": "gpt-5.4", "model_reasoning_effort": "high"}}
	router.applyThreadResumeCollaborationMode(response, cfg)

	if response.CollaborationMode == nil {
		t.Fatal("collaborationMode = nil, want the restored plan mode")
	}
	mode := response.CollaborationMode
	if mode.Mode != ModeKindPlan || mode.Settings.Model != "gpt-5.4" ||
		mode.Settings.ReasoningEffort == nil || *mode.Settings.ReasoningEffort != ReasoningEffort("high") {
		t.Fatalf("collaborationMode = %#v", mode)
	}
	if mode.Settings.DeveloperInstructions == nil || *mode.Settings.DeveloperInstructions != "plan first" {
		t.Fatalf("developer instructions = %#v, want the saved instructions", mode.Settings.DeveloperInstructions)
	}
	if response.ReasoningEffort == nil || *response.ReasoningEffort != "high" {
		t.Fatalf("reasoningEffort = %#v, want the effective effort", response.ReasoningEffort)
	}
	restored := router.threadSettingsForTurn("thread-plan")
	if restored == nil || restored.CollaborationMode == nil {
		t.Fatalf("restored settings = %#v", restored)
	}
	if stringFromAnyMapValue(restored.CollaborationMode, "mode") != "plan" {
		t.Fatalf("restored mode = %#v", restored.CollaborationMode)
	}
	restoredSettings, _ := restored.CollaborationMode["settings"].(map[string]any)
	if stringFromAnyMapValue(restoredSettings, "model") != "gpt-5.4" {
		t.Fatalf("restored model = %#v, want the effective model", restoredSettings["model"])
	}
	if stringFromAnyMapValue(restoredSettings, "reasoning_effort") != "high" {
		t.Fatalf("restored effort = %#v, want the effective effort", restoredSettings["reasoning_effort"])
	}
	// The thread-owned settings snapshot the next resume restores from carries
	// the overlaid mode (Rust persists the resumed settings snapshot).
	snapshot := router.threadCollaborationModeSnapshot("thread-plan")
	if !strings.Contains(string(snapshot), `"model":"gpt-5.4"`) || !strings.Contains(string(snapshot), `"reasoning_effort":"high"`) ||
		!strings.Contains(string(snapshot), `"mode":"plan"`) || !strings.Contains(string(snapshot), "plan first") {
		t.Fatalf("settings snapshot = %s", string(snapshot))
	}
}

// TestThreadResumeClearsSavedEffortWhenConfigHasNoneLikeRust covers Rust's
// `Some(None)` arm: the effective config has no reasoning effort, so the saved
// mode's effort is cleared while the mode and instructions survive.
func TestThreadResumeClearsSavedEffortWhenConfigHasNoneLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(session.NewStore(t.TempDir())),
		Config:       config.NewConfigService(t.TempDir()),
	})
	response := &ThreadResumeResponse{
		Thread:            &Thread{ID: "thread-plan"},
		CollaborationMode: collaborationModeFromRaw(json.RawMessage(planCollaborationModeJSON)),
	}
	cfg := &config.Config{Values: map[string]any{"model": "gpt-5.4"}}
	router.applyThreadResumeCollaborationMode(response, cfg)

	if response.CollaborationMode == nil || response.CollaborationMode.Mode != ModeKindPlan {
		t.Fatalf("collaborationMode = %#v", response.CollaborationMode)
	}
	if response.CollaborationMode.Settings.ReasoningEffort != nil {
		t.Fatalf("reasoningEffort = %#v, want the saved effort cleared", response.CollaborationMode.Settings.ReasoningEffort)
	}
	if response.ReasoningEffort != nil {
		t.Fatalf("response reasoningEffort = %#v, want nil", response.ReasoningEffort)
	}
}

// TestRuntimeRouterThreadResumeRestoresPersistedModeLikeRust mirrors Rust's
// thread_resume_preserves_acknowledged_model_effort_and_approvals_reviewer: a
// cold resume restores the thread-owned Plan mode, takes the resumed model and
// the config's reasoning effort, reports both, and appends a settings snapshot
// carrying the overlaid mode.
func TestRuntimeRouterThreadResumeRestoresPersistedModeLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte("model_reasoning_effort = \"high\"\n"), 0o600); err != nil {
		t.Fatalf("write config error = %v", err)
	}
	now := fixedTime()
	threadID := session.ThreadID("thread-resume-plan")
	store := session.NewStore(filepath.Join(home, "sessions"))
	threadRouter := NewRouter(store)
	record := &session.Record{
		ID: threadID, SessionID: string(threadID), CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: t.TempDir(), SessionPrefix: session.PrefixForSessionID(string(threadID))},
	}
	if err := store.Create(record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := threadRouter.createThreadRollout(record, now); err != nil {
		t.Fatalf("createThreadRollout() error = %v", err)
	}
	path := threadRouter.threadRolloutPath(record)
	recorder, err := rollout.Resume(path)
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if err := recorder.AppendThreadSettingsAppliedWithSnapshot(
		string(threadID), "never", "", json.RawMessage(planCollaborationModeJSON), now.Add(time.Second),
	); err != nil {
		_ = recorder.Close()
		t.Fatalf("AppendThreadSettingsAppliedWithSnapshot() error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: threadRouter,
		Config:       config.NewConfigService(home),
	})
	model := "gpt-5.4"
	resumed := router.Handle(requestWithParams(t, IntID(1), MethodThreadResume, ThreadResumeParams{
		ThreadID:     string(threadID),
		Model:        &model,
		ExcludeTurns: true,
	}))
	if resumed.Error != nil {
		t.Fatalf("resume error = %+v", resumed.Error)
	}
	response := resumed.Result.(*ThreadResumeResponse)
	if response.CollaborationMode == nil {
		t.Fatal("collaborationMode = nil, want the restored plan mode")
	}
	mode := response.CollaborationMode
	if mode.Mode != ModeKindPlan || mode.Settings.Model != model ||
		mode.Settings.ReasoningEffort == nil || *mode.Settings.ReasoningEffort != ReasoningEffort("high") ||
		mode.Settings.DeveloperInstructions == nil || *mode.Settings.DeveloperInstructions != "plan first" {
		t.Fatalf("collaborationMode = %#v", mode)
	}
	if response.ReasoningEffort == nil || *response.ReasoningEffort != "high" {
		t.Fatalf("reasoningEffort = %#v, want the effective effort", response.ReasoningEffort)
	}
	// The resume appends a thread-owned settings snapshot carrying the overlaid
	// mode, so the next resume restores it instead of the original snapshot.
	lines, _, err := rollout.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	persisted := rollout.LatestPersistedCollaborationMode(string(threadID), lines)
	if !strings.Contains(string(persisted), `"model":"gpt-5.4"`) ||
		!strings.Contains(string(persisted), `"reasoning_effort":"high"`) ||
		!strings.Contains(string(persisted), "plan first") {
		t.Fatalf("persisted collaboration mode = %s", string(persisted))
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
