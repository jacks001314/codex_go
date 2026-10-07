package appserver

import (
	"encoding/json"
	"testing"
	"time"

	"codex_go/rollout"
	"codex_go/session"
)

// TestRuntimeCompactionPersistsCheckpointResumeMetadataLikeRust mirrors Rust
// #51402 (`551bd409eb`, "Preserve turn attribution across recovery and
// compaction") on the Go runtime compaction path, after
// `compaction_persists_resume_metadata_and_companion_records`
// (Rust `codex-rs/core/src/session/tests.rs`).
//
// The manual compaction RPC writes a checkpoint that records the window it
// starts plus `resume_metadata{last_started_turn_id, turn_attribution}`, and a
// cold recovery of that rollout restores the newest regular turn's provenance
// from the checkpoint instead of falling back to the model-context root.
func TestRuntimeCompactionPersistsCheckpointResumeMetadataLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	const threadID = "thread-checkpoint-metadata"
	if err := store.Save(&session.Record{
		ID:        threadID,
		SessionID: threadID,
		Items: []session.Item{
			{ID: "u1", Type: "message", Role: "user", Text: "first request"},
			{ID: "a1", Type: "agent_message", Role: "assistant", Text: "first answer"},
		},
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	router.SetNotificationSink(NewNotificationBuffer())
	router.requireThreadStatus().UpsertThread(threadID, false)

	// The regular turn that preceded the compaction persisted its attribution on
	// the `turn_started` record, exactly as a live turn does before startup work
	// can be suspended.
	now := time.Now().UTC()
	recorder, err := router.openRuntimeRollout(threadID)
	if err != nil || recorder == nil {
		t.Fatalf("openRuntimeRollout() = %v, %v", recorder, err)
	}
	rolloutPath := recorder.Path()
	trigger, parent, root := "automation", "parent-turn", "root-turn"
	attribution := &rollout.TurnAttribution{
		TurnID:       "turn-1",
		TurnTrigger:  &trigger,
		ParentTurnID: &parent,
		RootTurnID:   &root,
	}
	if err := recorder.AppendTurnStartedWithAttribution(attribution, root, "turn-1", now); err != nil {
		t.Fatalf("AppendTurnStartedWithAttribution() error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadCompactStart, ThreadCompactStartParams{ThreadID: threadID}))
	if response.Error != nil {
		t.Fatalf("compact error: %+v", response.Error)
	}

	lines, _, err := rollout.Load(rolloutPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	payload := lastCompactedCheckpoint(t, lines)
	if payload.WindowNumber == nil || *payload.WindowNumber != 1 {
		t.Fatalf("checkpoint window_number = %v, want 1 (the window the compaction starts)", payload.WindowNumber)
	}
	if payload.ResumeMetadata == nil {
		t.Fatalf("checkpoint resume_metadata missing: %s", payload.Raw)
	}
	if payload.ResumeMetadata.LastStartedTurnID == nil {
		t.Fatalf("checkpoint last_started_turn_id missing: %s", payload.Raw)
	}
	if got := payload.ResumeMetadata.TurnAttribution; got == nil {
		t.Fatalf("checkpoint turn_attribution missing: %s", payload.Raw)
	} else if got.TurnID != "turn-1" || got.TurnTriggerValue() != "automation" || got.RootTurnIDValue() != "root-turn" {
		t.Fatalf("checkpoint turn_attribution = %#v, want the preceded regular turn", got)
	}

	// The window the compaction starts is persisted on the record too, mirroring
	// the runtime path's in-memory counter.
	record, err := store.Read(threadID, true, false)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if got := intFromAny(record.Metadata.Extra["auto_compact_window_number"]); got != 1 {
		t.Fatalf("persisted window number = %v, want 1", record.Metadata.Extra["auto_compact_window_number"])
	}

	// Cold recovery reads the checkpoint back: the recovered turn restores the
	// original trigger and lineage instead of the model-context root fallback.
	cold, err := rollout.RecordFromPath(rolloutPath, false)
	if err != nil {
		t.Fatalf("RecordFromPath() error = %v", err)
	}
	options := cold.RecoveredTurnStartOptions("turn-1")
	if options.TurnTrigger != "automation" || options.ParentTurnID != "parent-turn" || options.RootTurnID != "root-turn" {
		t.Fatalf("recovered turn start options = %#v, want the checkpoint attribution", options)
	}
}

type compactedPayload struct {
	WindowNumber   *uint64 `json:"window_number"`
	ResumeMetadata *struct {
		LastStartedTurnID *string                  `json:"last_started_turn_id"`
		TurnAttribution   *session.TurnAttribution `json:"turn_attribution"`
	} `json:"resume_metadata"`
	Raw []byte `json:"-"`
}

func lastCompactedCheckpoint(t *testing.T, lines []rollout.Line) compactedPayload {
	t.Helper()
	for i := len(lines) - 1; i >= 0; i-- {
		if lines[i].Type != "compacted" {
			continue
		}
		var payload compactedPayload
		if err := json.Unmarshal(lines[i].Payload, &payload); err != nil {
			t.Fatalf("decode compacted payload: %v", err)
		}
		payload.Raw = lines[i].Payload
		return payload
	}
	t.Fatalf("no compacted line in %d lines", len(lines))
	return compactedPayload{}
}

// TestRouterThreadCompactStartCheckpointResumeMetadataLikeRust covers the
// Router-level `thread/compact/start` implementation (the in-process app
// app-server path) against the same Rust #51402 semantics: the checkpoint it
// appends carries the window it starts and the resume metadata, so a cold
// recovery restores the preceded regular turn's provenance.
func TestRouterThreadCompactStartCheckpointResumeMetadataLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	now := fixedTime()
	if err := store.Save(&session.Record{
		ID:        "thread-router-checkpoint",
		SessionID: "thread-router-checkpoint",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Items: []session.Item{
			{ID: "u1", Type: "message", Role: "user", Text: "first request", CreatedAt: now},
			{ID: "a1", Type: "agent_message", Role: "assistant", Text: "first answer", CreatedAt: now},
		},
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := router.createThreadRollout(&session.Record{ID: "thread-router-checkpoint", SessionID: "thread-router-checkpoint"}, now); err != nil {
		t.Fatalf("create rollout error: %v", err)
	}
	path, err := rollout.FindThreadPath(store.Root(), "thread-router-checkpoint", false)
	if err != nil {
		t.Fatalf("rollout path error: %v", err)
	}
	trigger, parent, root := "user", "parent-turn", "root-turn"
	attribution := &rollout.TurnAttribution{
		TurnID:       "turn-1",
		TurnTrigger:  &trigger,
		ParentTurnID: &parent,
		RootTurnID:   &root,
	}
	recorder, err := rollout.Resume(path)
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if err := recorder.AppendTurnStartedWithAttribution(attribution, root, "turn-1", now); err != nil {
		t.Fatalf("AppendTurnStartedWithAttribution() error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadCompactStart, ThreadCompactStartParams{ThreadID: "thread-router-checkpoint"}))
	if response.Error != nil {
		t.Fatalf("compact error: %+v", response.Error)
	}

	lines, _, err := rollout.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	payload := lastCompactedCheckpoint(t, lines)
	if payload.WindowNumber == nil || *payload.WindowNumber != 1 {
		t.Fatalf("checkpoint window_number = %v, want 1", payload.WindowNumber)
	}
	if payload.ResumeMetadata == nil || payload.ResumeMetadata.TurnAttribution == nil {
		t.Fatalf("checkpoint resume metadata missing: %s", payload.Raw)
	}
	if got := payload.ResumeMetadata.TurnAttribution; got.TurnID != "turn-1" || got.TurnTriggerValue() != "user" || got.RootTurnIDValue() != "root-turn" {
		t.Fatalf("checkpoint turn_attribution = %#v, want the preceded regular turn", got)
	}

	record, err := store.Read("thread-router-checkpoint", true, false)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if got := intFromAny(record.Metadata.Extra["auto_compact_window_number"]); got != 1 {
		t.Fatalf("persisted window number = %v, want 1", record.Metadata.Extra["auto_compact_window_number"])
	}

	cold, err := rollout.RecordFromPath(path, false)
	if err != nil {
		t.Fatalf("RecordFromPath() error = %v", err)
	}
	options := cold.RecoveredTurnStartOptions("turn-1")
	if options.TurnTrigger != "user" || options.RootTurnID != "root-turn" {
		t.Fatalf("recovered turn start options = %#v, want the checkpoint attribution", options)
	}
}
