package rollout

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"codex_go/session"
)

// These tests mirror Rust #51402 (`551bd409eb`) "Preserve turn attribution
// across recovery and compaction", specifically the scenarios in
// `codex-rs/core/src/session/rollout_reconstruction_tests.rs` and
// `codex-rs/core/src/session/tests.rs`:
//
//   - suspension before context persistence (a `turn_started` with attribution
//     but no input or context),
//   - fresh attribution on subsequent turns,
//   - legacy rollouts without attribution,
//   - late terminal events that belong to a preceding turn,
//   - compaction checkpoints and rollback.

func attributionPtr(turnID string, fields map[string]string) *session.TurnAttribution {
	attribution := &session.TurnAttribution{TurnID: turnID}
	if value, ok := fields["turn_trigger"]; ok {
		trigger := value
		attribution.TurnTrigger = &trigger
	}
	if value, ok := fields["parent_turn_id"]; ok {
		parent := value
		attribution.ParentTurnID = &parent
	}
	if value, ok := fields["root_turn_id"]; ok {
		root := value
		attribution.RootTurnID = &root
	}
	if value, ok := fields["initiating_agent_path"]; ok {
		path := value
		attribution.InitiatingAgentPath = &path
	}
	return attribution
}

func turnStartedLine(t *testing.T, turnID string, attribution *session.TurnAttribution) Line {
	t.Helper()
	payload := map[string]any{"type": "task_started", "turn_id": turnID, "started_at": 10}
	if attribution != nil {
		payload["turn_attribution"] = attribution
	}
	return mustEventLine(t, payload)
}

// completedUserTurnRollout mirrors Rust's `completed_user_turn_rollout`:
// `TurnStarted`, a user message, the turn context, and `TurnComplete`.
func completedUserTurnRollout(t *testing.T, turnID string, attribution *session.TurnAttribution) []Line {
	t.Helper()
	return []Line{
		turnStartedLine(t, turnID, attribution),
		responseItemLine(t, map[string]any{
			"type": "message", "role": "user",
			"content": []any{map[string]any{"type": "input_text", "text": "seed"}},
		}),
		turnContextLine(t, map[string]any{"turn_id": turnID}),
		mustEventLine(t, map[string]any{"type": "task_complete", "turn_id": turnID}),
	}
}

func mustEventLine(t *testing.T, payload map[string]any) Line {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal event payload: %v", err)
	}
	return Line{Type: "event_msg", Payload: raw}
}

func responseItemLine(t *testing.T, item map[string]any) Line {
	t.Helper()
	raw, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("marshal response item: %v", err)
	}
	return Line{Type: "item", Item: raw}
}

func turnContextLine(t *testing.T, values map[string]any) Line {
	t.Helper()
	raw, err := json.Marshal(values)
	if err != nil {
		t.Fatalf("marshal turn context: %v", err)
	}
	return Line{Type: "turn_context", TurnContext: raw}
}

func compactedCheckpointLine(t *testing.T, values map[string]any) Line {
	t.Helper()
	raw, err := json.Marshal(values)
	if err != nil {
		t.Fatalf("marshal compacted line: %v", err)
	}
	return Line{Type: "compacted", Payload: raw}
}

func rollbackLine(numTurns uint32) Line {
	return Line{Type: "event_msg", ThreadRolledBack: &RollbackEvent{NumTurns: numTurns}}
}

func requireAttribution(t *testing.T, got *session.TurnAttribution, want *session.TurnAttribution) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		gotJSON, _ := json.Marshal(got)
		wantJSON, _ := json.Marshal(want)
		t.Fatalf("turn attribution = %s, want %s", gotJSON, wantJSON)
	}
}

// Rust `reconstruct_attribution_ignores_previous_turn_terminal` /
// `AttributionEvidence::StartAndContext`: the newest surviving turn keeps its own
// attribution even though a preceding turn's terminal event arrives after it.
func TestReconstructTurnAttributionIgnoresPreviousTurnTerminalLikeRust(t *testing.T) {
	older := attributionPtr("older-turn", map[string]string{"turn_trigger": "composer", "root_turn_id": "root-turn"})
	expected := attributionPtr("current-turn", map[string]string{
		"turn_trigger": "automation", "parent_turn_id": "calling-turn",
		"root_turn_id": "root-turn", "initiating_agent_path": "/root/requester",
	})

	previous := completedUserTurnRollout(t, "older-turn", older)
	lateTerminal := previous[len(previous)-1]
	previous = previous[:len(previous)-1]

	current := completedUserTurnRollout(t, "current-turn", expected)
	current = current[:len(current)-1]

	lines := append(append(append([]Line{}, previous...), current...), lateTerminal)
	requireAttribution(t, ReconstructTurnAttribution(lines), expected)

	// A rollback of the newer user turn restores the preceding turn's attribution.
	rolledBack := append(append([]Line{}, lines...), rollbackLine(1))
	requireAttribution(t, ReconstructTurnAttribution(rolledBack), older)
}

// Rust `AttributionEvidence::StartOnly`: a late abort for the preceding turn is
// not attributed to the current segment, which carries its own start
// attribution without any context record.
func TestReconstructTurnAttributionIgnoresPreviousTurnLateAbortLikeRust(t *testing.T) {
	older := attributionPtr("older-turn", map[string]string{"turn_trigger": "composer"})
	expected := attributionPtr("current-turn", map[string]string{"turn_trigger": "automation", "root_turn_id": "root-turn"})

	previous := completedUserTurnRollout(t, "older-turn", older)
	previous = previous[:len(previous)-1]

	lines := append(append([]Line{}, previous...), turnStartedLine(t, "current-turn", expected))
	lines = append(lines, mustEventLine(t, map[string]any{"type": "turn_aborted", "turn_id": "older-turn", "reason": "interrupted"}))

	requireAttribution(t, ReconstructTurnAttribution(lines), expected)
}

// Rust `reconstruct_attribution_ignores_previous_terminal_after_checkpoint`
// (cases "continuation marker retained" / "cleared"): the compaction
// checkpoint's attribution identifies the turn even when the continuation
// marker is absent, and a rollback still consumes it.
func TestReconstructTurnAttributionIgnoresPreviousTerminalAfterCheckpointLikeRust(t *testing.T) {
	expected := attributionPtr("current-turn", map[string]string{"turn_trigger": "automation", "parent_turn_id": "calling-turn", "root_turn_id": "root-turn"})

	for _, testCase := range []struct {
		name             string
		lastStartedValue any
	}{
		{name: "continuation marker retained", lastStartedValue: "current-turn"},
		{name: "continuation marker cleared", lastStartedValue: nil},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			resumeMetadata := map[string]any{"turn_attribution": expected}
			if testCase.lastStartedValue != nil {
				resumeMetadata["last_started_turn_id"] = testCase.lastStartedValue
			}
			lines := []Line{
				compactedCheckpointLine(t, map[string]any{
					"message":             "summary",
					"replacement_history": []any{map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "original work"}}}},
					"window_number":       1,
					"resume_metadata":     resumeMetadata,
				}),
				responseItemLine(t, map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "continued work"}}}),
				turnContextLine(t, map[string]any{"turn_id": "current-turn", "root_turn_id": "root-turn"}),
				mustEventLine(t, map[string]any{"type": "task_complete", "turn_id": "older-turn"}),
			}
			requireAttribution(t, ReconstructTurnAttribution(lines), expected)

			rolledBack := append(append([]Line{}, lines...), rollbackLine(1))
			requireAttribution(t, ReconstructTurnAttribution(rolledBack), nil)
		})
	}
}

// Rust `completed_turn_suffix_after_compaction_overrides_resume_metadata`:
// a completed turn after the checkpoint wins, a fresh turn started after it
// wins, and a rollback of the newer turn restores the checkpoint's older
// regular turn.
func TestReconstructTurnAttributionCompletedTurnSuffixAfterCompactionLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name             string
		lateCompletion   bool
		legacyCheckpoint bool
	}{
		{name: "completion before next turn"},
		{name: "completion after next turn starts", lateCompletion: true},
		{name: "late completion without checkpoint turn identity", lateCompletion: true, legacyCheckpoint: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			newer := attributionPtr("newer-turn", map[string]string{"turn_trigger": "automation"})
			var checkpointAttribution *session.TurnAttribution
			if !testCase.legacyCheckpoint {
				checkpointAttribution = newer
			}
			resumeMetadata := map[string]any{
				"turn_attribution":       checkpointAttribution,
				"previous_turn_settings": map[string]any{"model": "metadata-model", "comp_hash": "metadata-hash"},
				"last_started_turn_id":   nil,
			}
			if checkpointAttribution != nil {
				resumeMetadata["last_started_turn_id"] = "newer-turn"
			}
			lines := []Line{
				compactedCheckpointLine(t, map[string]any{
					"message":             "summary",
					"replacement_history": []any{map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "seed"}}}},
					"window_number":       1,
					"resume_metadata":     resumeMetadata,
				}),
				turnContextLine(t, map[string]any{"turn_id": "newer-turn", "model": "newer-model", "comp_hash": "newer-hash"}),
			}

			var nextAttribution *session.TurnAttribution
			if testCase.lateCompletion {
				nextAttribution = attributionPtr("next-turn", map[string]string{"turn_trigger": "user"})
				lines = append(lines, turnStartedLine(t, "next-turn", nextAttribution))
			}

			want := nextAttribution
			if want == nil {
				want = checkpointAttribution
			}
			requireAttribution(t, ReconstructTurnAttribution(lines), want)

			// A rollback of a newer user turn restores the checkpoint's older regular turn.
			older := attributionPtr("older-turn", map[string]string{"turn_trigger": "automation"})
			rolledBack := append([]Line{compactedCheckpointLine(t, map[string]any{
				"message":             "summary",
				"replacement_history": []any{map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "seed"}}}},
				"window_number":       1,
				"resume_metadata": map[string]any{
					"turn_attribution":     older,
					"last_started_turn_id": "newer-turn",
				},
			})}, completedUserTurnRollout(t, "newer-turn", nil)...)
			rolledBack = append(rolledBack, rollbackLine(1))
			requireAttribution(t, ReconstructTurnAttribution(rolledBack), older)
		})
	}
}

// Rust `compaction_persists_resume_metadata_and_companion_records`: a
// checkpoint whose continuation marker is cleared or replaced by a standalone
// compaction turn still yields the regular turn's attribution, and a rollback
// drops it.
func TestReconstructTurnAttributionCheckpointSurvivesContinuationMarkerChangesLikeRust(t *testing.T) {
	expected := attributionPtr("checkpoint-turn", map[string]string{"turn_trigger": "automation", "parent_turn_id": "parent-turn", "initiating_agent_path": "/root/requester", "root_turn_id": "root-turn"})
	for _, testCase := range []struct {
		name        string
		markerValue any
	}{
		{name: "continuation eligibility cleared", markerValue: nil},
		{name: "standalone compaction replaced the id", markerValue: "compact-turn"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			resumeMetadata := map[string]any{"turn_attribution": expected, "last_started_turn_id": testCase.markerValue}
			lines := []Line{compactedCheckpointLine(t, map[string]any{
				"message":             "summary",
				"replacement_history": []any{map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "seed"}}}},
				"window_number":       1,
				"resume_metadata":     resumeMetadata,
			})}
			if testCase.markerValue != nil {
				lines = append(lines, mustEventLine(t, map[string]any{"type": "task_complete", "turn_id": testCase.markerValue}))
			}
			requireAttribution(t, ReconstructTurnAttribution(lines), expected)

			rolledBack := append(append([]Line{}, lines...), rollbackLine(1))
			requireAttribution(t, ReconstructTurnAttribution(rolledBack), nil)
		})
	}
}

// A turn suspended before any input or context is written still carries its
// attribution (Rust "suspension before context persistence"), and a legacy
// record without one reconstructs nothing (Rust "legacy rollouts").
func TestReconstructTurnAttributionSuspensionAndLegacyRolloutLikeRust(t *testing.T) {
	expected := attributionPtr("turn-1", map[string]string{"turn_trigger": "user", "root_turn_id": "turn-1"})
	requireAttribution(t, ReconstructTurnAttribution([]Line{turnStartedLine(t, "turn-1", expected)}), expected)
	requireAttribution(t, ReconstructTurnAttribution([]Line{turnStartedLine(t, "turn-1", nil)}), nil)
}

// "Fresh attribution on subsequent turns": the newest started turn wins.
func TestReconstructTurnAttributionFreshOnSubsequentTurnsLikeRust(t *testing.T) {
	first := attributionPtr("turn-1", map[string]string{"turn_trigger": "user"})
	second := attributionPtr("turn-2", map[string]string{"turn_trigger": "automation", "parent_turn_id": "turn-1"})
	lines := append(completedUserTurnRollout(t, "turn-1", first), completedUserTurnRollout(t, "turn-2", second)...)
	requireAttribution(t, ReconstructTurnAttribution(lines), second)
}

// The record exposes the recovered provenance, and an unknown trigger stays
// absent instead of being replaced with `retry` (Rust #51402 removed the forced
// `turn_trigger: Some("retry")` from `handle_recovery`).
func TestRecoveredTurnStartOptionsNeverSubstitutesRetryLikeRust(t *testing.T) {
	record := &session.Record{Metadata: session.Metadata{
		TurnAttribution: attributionPtr("turn-1", map[string]string{"parent_turn_id": "turn-0", "initiating_agent_path": "/root/requester"}),
	}}
	options := record.RecoveredTurnStartOptions("turn-1")
	if options.TurnTrigger != "" {
		t.Fatalf("recovered trigger = %q, want absent (never `retry`)", options.TurnTrigger)
	}
	if options.ParentTurnID != "turn-0" || options.InitiatingAgentPath != "/root/requester" {
		t.Fatalf("recovered lineage = %#v", options)
	}

	// A turn the attribution does not name falls back to the model-context root.
	legacy := &session.Record{Metadata: session.Metadata{
		TurnAttribution: attributionPtr("turn-1", nil),
		TurnContext:     json.RawMessage(`{"turn_id":"turn-2","root_turn_id":"root-2"}`),
	}}
	fallback := legacy.RecoveredTurnStartOptions("turn-2")
	if fallback.RootTurnID != "root-2" || fallback.TurnTrigger != "" {
		t.Fatalf("legacy fallback = %#v, want root-2 with an absent trigger", fallback)
	}
	if mismatch := legacy.RecoveredTurnStartOptions("turn-3"); mismatch != (session.TurnStartOptions{}) {
		t.Fatalf("non-matching turn = %#v, want empty options", mismatch)
	}
}

// Rust #51402 persists attribution on regular turns only; the emission helper
// writes it when present and omits it otherwise (`skip_serializing_if`).
func TestAppendTurnStartedAttributionEmissionLikeRust(t *testing.T) {
	dir := t.TempDir()
	recorder, err := NewRecorder(&CreateParams{CodexHome: dir, ThreadID: "thread-1", HistoryMode: "paginated"})
	if err != nil {
		t.Fatalf("NewRecorder error = %v", err)
	}
	attribution := attributionPtr("turn-1", map[string]string{"turn_trigger": "user"})
	if err := recorder.AppendTurnStartedWithAttribution(attribution, "turn-1", "turn-1", time.Now().UTC()); err != nil {
		t.Fatalf("AppendTurnStartedWithAttribution error = %v", err)
	}
	if err := recorder.AppendTurnStartedWithRoot("turn-2", "turn-2", time.Now().UTC()); err != nil {
		t.Fatalf("AppendTurnStartedWithRoot error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}

	lines, _, err := Load(recorder.Path())
	if err != nil {
		t.Fatalf("Load error = %v", err)
	}

	var first, second rolloutEventPayload
	for i := range lines {
		if lines[i].Type != "event_msg" {
			continue
		}
		var payload rolloutEventPayload
		if json.Unmarshal(lines[i].Payload, &payload) != nil {
			continue
		}
		switch firstNonEmptyString(payload.TurnID, payload.TurnIDCamel) {
		case "turn-1":
			first = payload
		case "turn-2":
			second = payload
		}
	}
	if first.TurnAttribution == nil || first.TurnAttribution.TurnTriggerValue() != "user" {
		t.Fatalf("regular turn attribution = %#v", first.TurnAttribution)
	}
	if second.TurnAttribution != nil {
		t.Fatalf("non-regular turn attribution = %#v, want absent", second.TurnAttribution)
	}
}
