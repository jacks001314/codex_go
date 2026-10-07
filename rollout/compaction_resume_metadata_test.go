package rollout

import (
	"encoding/json"
	"testing"

	"codex_go/session"
)

// TestAppendCompactedWithResumeMetadataLikeRust pins the writer half of Rust
// #51402 (`551bd409eb`, "Preserve turn attribution across recovery and
// compaction"), mirroring `compaction_persists_resume_metadata_and_companion_records`
// (Rust `codex-rs/core/src/session/tests.rs`) and
// `completed_turn_suffix_after_compaction_overrides_resume_metadata`
// (`rollout_reconstruction_tests.rs`).
//
// A checkpoint persists the window it starts (`CompactedItem::window_number`)
// together with `resume_metadata{last_started_turn_id, turn_attribution}`, and a
// rollout that survives only as that checkpoint still reconstructs the newest
// regular turn's provenance: recovery replays only the suffix after the newest
// compaction, so the checkpoint is that attribution's only source.
func TestAppendCompactedWithResumeMetadataLikeRust(t *testing.T) {
	now := fixedTime()
	attribution := attributionPtr("turn-1", map[string]string{
		"turn_trigger":   "automation",
		"parent_turn_id": "parent-turn",
		"root_turn_id":   "root-turn",
	})
	lastStarted := "compact-turn"
	windowNumber := uint64(1)

	recorder, err := NewRecorder(&CreateParams{
		CodexHome:     t.TempDir(),
		ThreadID:      "thread-resume-metadata",
		SessionID:     "thread-resume-metadata",
		Source:        "cli",
		ModelProvider: "openai",
		Now:           now,
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	path := recorder.Path()
	// The read side only accepts a checkpoint that carries its replacement
	// history (`selectAttributionCheckpoint`), so the writer must persist one.
	compactedContext := ItemFromSessionItem(&session.Item{ID: "compacted-context", Type: "message", Role: "user", Text: "compacted context"})
	resume := &CompactionResumeMetadata{LastStartedTurnID: &lastStarted, TurnAttribution: attribution}
	if err := recorder.AppendCompactedWithResumeMetadata("summary", []Item{*compactedContext}, nil, resume, &windowNumber, now); err != nil {
		t.Fatalf("AppendCompactedWithResumeMetadata() error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	lines, _, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	payload := lastCompactedPayload(t, lines)
	if payload.WindowNumber == nil || *payload.WindowNumber != windowNumber {
		t.Fatalf("checkpoint window_number = %v, want %d", payload.WindowNumber, windowNumber)
	}
	if payload.ResumeMetadata == nil {
		t.Fatalf("checkpoint resume_metadata missing: %s", payload.Raw)
	}
	if payload.ResumeMetadata.LastStartedTurnID == nil || *payload.ResumeMetadata.LastStartedTurnID != lastStarted {
		t.Fatalf("checkpoint last_started_turn_id = %v, want %q", payload.ResumeMetadata.LastStartedTurnID, lastStarted)
	}
	requireAttribution(t, payload.ResumeMetadata.TurnAttribution, attribution)

	// The written checkpoint is what a cold resume reads back, and it is the
	// only source in this rollout: there is no `turn_started` line to fall back
	// to (Rust replays just the checkpoint's suffix).
	requireAttribution(t, ReconstructTurnAttribution(lines), attribution)
	record, err := RecordFromPath(path, false)
	if err != nil {
		t.Fatalf("RecordFromPath() error = %v", err)
	}
	requireAttribution(t, record.Metadata.TurnAttribution, attribution)
	options := record.RecoveredTurnStartOptions("turn-1")
	if options.TurnTrigger != "automation" || options.ParentTurnID != "parent-turn" || options.RootTurnID != "root-turn" {
		t.Fatalf("recovered turn start options = %#v, want the checkpoint attribution", options)
	}

	// Reverse control: the pre-#51402 writer leaves the checkpoint without a
	// window or resume metadata, so the identical reconstruction finds nothing.
	// Removing the writer wiring therefore flips the assertions above.
	legacy, err := NewRecorder(&CreateParams{
		CodexHome:     t.TempDir(),
		ThreadID:      "thread-legacy-checkpoint",
		SessionID:     "thread-legacy-checkpoint",
		Source:        "cli",
		ModelProvider: "openai",
		Now:           now,
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	legacyPath := legacy.Path()
	if err := legacy.AppendCompacted("summary", nil, now); err != nil {
		t.Fatalf("AppendCompacted() error = %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	legacyLines, _, err := Load(legacyPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	legacyPayload := lastCompactedPayload(t, legacyLines)
	if legacyPayload.WindowNumber != nil || legacyPayload.ResumeMetadata != nil {
		t.Fatalf("legacy checkpoint carries resume state: %s", legacyPayload.Raw)
	}
	requireAttribution(t, ReconstructTurnAttribution(legacyLines), nil)
	legacyRecord, err := RecordFromPath(legacyPath, false)
	if err != nil {
		t.Fatalf("RecordFromPath() error = %v", err)
	}
	if options := legacyRecord.RecoveredTurnStartOptions("turn-1"); options != (session.TurnStartOptions{}) {
		t.Fatalf("legacy checkpoint recovered options = %#v, want none", options)
	}
}

type compactedCheckpointPayload struct {
	WindowNumber   *uint64 `json:"window_number"`
	ResumeMetadata *struct {
		LastStartedTurnID *string                  `json:"last_started_turn_id"`
		TurnAttribution   *session.TurnAttribution `json:"turn_attribution"`
	} `json:"resume_metadata"`
	Raw []byte `json:"-"`
}

func lastCompactedPayload(t *testing.T, lines []Line) compactedCheckpointPayload {
	t.Helper()
	for i := len(lines) - 1; i >= 0; i-- {
		if lines[i].Type != "compacted" {
			continue
		}
		var payload compactedCheckpointPayload
		if err := json.Unmarshal(lines[i].Payload, &payload); err != nil {
			t.Fatalf("decode compacted payload: %v", err)
		}
		payload.Raw = lines[i].Payload
		return payload
	}
	t.Fatalf("no compacted line in %d lines", len(lines))
	return compactedCheckpointPayload{}
}
