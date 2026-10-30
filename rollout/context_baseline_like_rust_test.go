package rollout

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func contextBaselineEventLine(t *testing.T, eventType string, turnID string) Line {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"type": eventType, "turn_id": turnID})
	if err != nil {
		t.Fatalf("Marshal(%s) error = %v", eventType, err)
	}
	return Line{Type: "event_msg", Payload: payload}
}

func contextBaselineUserItemLine(t *testing.T, text string) Line {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"type":    "message",
		"role":    "user",
		"content": []map[string]any{{"type": "input_text", "text": text}},
	})
	if err != nil {
		t.Fatalf("Marshal(user item) error = %v", err)
	}
	return Line{Type: "item", Item: raw}
}

func contextBaselineTurnContextLine(t *testing.T, turnID string, model string) Line {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"turn_id": turnID, "model": model, "comp_hash": "hash-" + model})
	if err != nil {
		t.Fatalf("Marshal(turn_context) error = %v", err)
	}
	return Line{Type: "turn_context", TurnContext: raw}
}

func contextBaselineWorldStateLine(t *testing.T, full bool, state map[string]any) Line {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"full": full, "state": state})
	if err != nil {
		t.Fatalf("Marshal(world_state) error = %v", err)
	}
	return Line{Type: "world_state", WorldState: raw}
}

func contextBaselineCompactedLine(t *testing.T) Line {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"message": "summary", "replacement_history": []any{}})
	if err != nil {
		t.Fatalf("Marshal(compacted) error = %v", err)
	}
	return Line{Type: "compacted", Payload: payload}
}

func assertContextBaselineTurnContext(t *testing.T, baseline ContextBaseline, wantModel string, want bool) {
	t.Helper()
	model, _, _, ok := TurnContextSettings(baseline.TurnContext)
	if ok != want {
		t.Fatalf("TurnContextSettings() ok = %t, want %t (payload %s)", ok, want, baseline.TurnContext)
	}
	if want && model != wantModel {
		t.Fatalf("recovered model = %q, want %q", model, wantModel)
	}
}

// Mirrors Rust's `record_initial_history_restores_world_state_baseline`: a full
// world-state snapshot is a context baseline both for a completed user turn and
// when a fork removed the task message.
func TestReconstructContextBaselineRestoresFullSnapshotLikeRust(t *testing.T) {
	cases := map[string]bool{
		"user turn":                 true,
		"fork removed task message": false,
	}
	for name, withUserMessage := range cases {
		t.Run(name, func(t *testing.T) {
			lines := []Line{
				contextBaselineEventLine(t, "turn_started", "turn-1"),
			}
			if withUserMessage {
				lines = append(lines, contextBaselineUserItemLine(t, "Inspect the workspace."))
			}
			lines = append(lines,
				contextBaselineTurnContextLine(t, "turn-1", "fork-model"),
				contextBaselineWorldStateLine(t, true, map[string]any{"alpha": "beta"}),
				contextBaselineEventLine(t, "turn_complete", "turn-1"),
			)

			baseline := ReconstructContextBaseline(lines)
			assertContextBaselineTurnContext(t, baseline, "fork-model", true)
			if got := string(baseline.WorldState); got != `{"alpha":"beta"}` {
				t.Fatalf("world state = %s, want {\"alpha\":\"beta\"}", got)
			}
		})
	}
}

// Mirrors Rust's
// `record_initial_history_requires_surviving_full_snapshot_without_user_turn`:
// a bare turn context, a patch without a full snapshot, and a full snapshot
// superseded by compaction do not seed the baseline.
func TestReconstructContextBaselineRequiresSurvivingFullSnapshotLikeRust(t *testing.T) {
	cases := map[string][]Line{
		"bare turn context": {
			contextBaselineEventLine(t, "turn_started", "turn-1"),
			contextBaselineTurnContextLine(t, "turn-1", "fork-model"),
		},
		"patch without a full snapshot": {
			contextBaselineEventLine(t, "turn_started", "turn-1"),
			contextBaselineWorldStateLine(t, false, map[string]any{"alpha": "beta"}),
			contextBaselineTurnContextLine(t, "turn-1", "fork-model"),
		},
		"full snapshot before compaction": {
			contextBaselineEventLine(t, "turn_started", "turn-1"),
			contextBaselineWorldStateLine(t, true, map[string]any{"alpha": "beta"}),
			contextBaselineCompactedLine(t),
			contextBaselineTurnContextLine(t, "turn-1", "fork-model"),
		},
	}
	for name, lines := range cases {
		t.Run(name, func(t *testing.T) {
			baseline := ReconstructContextBaseline(lines)
			assertContextBaselineTurnContext(t, baseline, "", false)
			if baseline.WorldState != nil {
				t.Fatalf("world state = %s, want none", baseline.WorldState)
			}
		})
	}
}

// Mirrors Rust's world-state replay: full snapshots reset the baseline, patches
// merge onto it, and a patch without a live baseline is ignored.
func TestReconstructContextBaselineReplaysWorldStatePatchesLikeRust(t *testing.T) {
	lines := []Line{
		contextBaselineWorldStateLine(t, true, map[string]any{
			"tools": map[string]any{"alpha": 1},
			"perm":  "read-only",
		}),
		contextBaselineWorldStateLine(t, false, map[string]any{
			"tools": map[string]any{"beta": 2},
			"perm":  nil,
		}),
	}
	baseline := ReconstructContextBaseline(lines)
	if got := string(baseline.WorldState); got != `{"tools":{"alpha":1,"beta":2}}` {
		t.Fatalf("world state = %s, want the merged snapshot", got)
	}

	orphanPatch := []Line{
		contextBaselineWorldStateLine(t, false, map[string]any{"alpha": "beta"}),
	}
	if got := ReconstructContextBaseline(orphanPatch).WorldState; got != nil {
		t.Fatalf("orphan patch produced %s, want none", got)
	}
}

// Mirrors `<reconstruct_history_rollback_skips_non_user_turns...>` for the
// baseline half: rollback discards every segment above the boundary, including
// the non-user standalone turn, and spends its budget on the newest user turns.
func TestReconstructContextBaselineRollsBackNewestUserTurnsLikeRust(t *testing.T) {
	userTurn := func(turnID string, model string, state map[string]any) []Line {
		return []Line{
			contextBaselineEventLine(t, "turn_started", turnID),
			contextBaselineUserItemLine(t, "Inspect the workspace."),
			contextBaselineTurnContextLine(t, turnID, model),
			contextBaselineWorldStateLine(t, true, state),
			contextBaselineEventLine(t, "turn_complete", turnID),
		}
	}
	standaloneTurn := []Line{
		contextBaselineEventLine(t, "turn_started", "standalone-turn"),
		contextBaselineWorldStateLine(t, true, map[string]any{"alpha": "standalone"}),
		contextBaselineTurnContextLine(t, "standalone-turn", "standalone-model"),
		contextBaselineEventLine(t, "turn_complete", "standalone-turn"),
	}
	rollback := Line{ThreadRolledBack: &RollbackEvent{NumTurns: 1}}

	var lines []Line
	lines = append(lines, userTurn("turn-1", "user-model", map[string]any{"alpha": "first"})...)
	lines = append(lines, userTurn("turn-2", "rolled-back-model", map[string]any{"alpha": "second"})...)
	lines = append(lines, standaloneTurn...)
	lines = append(lines, rollback)
	baseline := ReconstructContextBaseline(lines)
	assertContextBaselineTurnContext(t, baseline, "user-model", true)
	if got := string(baseline.WorldState); got != `{"alpha":"first"}` {
		t.Fatalf("world state = %s, want turn 1's snapshot", got)
	}

	rolledBackLines := append(userTurn("turn-1", "user-model", map[string]any{"alpha": "first"}), rollback)
	rolledBack := ReconstructContextBaseline(rolledBackLines)
	assertContextBaselineTurnContext(t, rolledBack, "", false)
	if rolledBack.WorldState != nil {
		t.Fatalf("world state = %s, want none after rollback", rolledBack.WorldState)
	}
}

// The rollout loader applies the reconstructed baseline, so a cold resume keeps
// the previous turn settings a child fork inherited from its full snapshot.
func TestRecordFromPathRestoresContextBaselineLikeRust(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	cases := map[string]struct {
		userMessage bool
		want        bool
	}{
		"user turn":                 {userMessage: true, want: true},
		"fork removed task message": {userMessage: false, want: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			recorder, err := NewRecorder(&CreateParams{
				CodexHome: t.TempDir(),
				ThreadID:  "thread-context-baseline",
				Now:       now,
			})
			if err != nil {
				t.Fatalf("NewRecorder() error = %v", err)
			}
			if err := recorder.AppendTurnStarted("turn-1", now); err != nil {
				t.Fatalf("AppendTurnStarted() error = %v", err)
			}
			if tc.userMessage {
				if err := recorder.AppendItem(Item{Type: "message", Role: "user", Text: "Inspect."}); err != nil {
					t.Fatalf("AppendItem() error = %v", err)
				}
			}
			if err := recorder.AppendTurnContext(TurnContextRecord{TurnID: "turn-1", Model: "gpt-fork"}, now); err != nil {
				t.Fatalf("AppendTurnContext() error = %v", err)
			}
			if err := recorder.AppendWorldState(json.RawMessage(`{"alpha":"beta"}`), now); err != nil {
				t.Fatalf("AppendWorldState() error = %v", err)
			}
			if err := recorder.AppendTurnComplete("turn-1", now, 0); err != nil {
				t.Fatalf("AppendTurnComplete() error = %v", err)
			}
			if err := recorder.Close(); err != nil {
				t.Fatalf("Close() error = %v", err)
			}

			record, err := RecordFromPath(recorder.Path(), false)
			if err != nil {
				t.Fatalf("RecordFromPath() error = %v", err)
			}
			model, _, _, ok := TurnContextSettings(record.Metadata.TurnContext)
			if ok != tc.want {
				t.Fatalf("TurnContextSettings() ok = %t, want %t", ok, tc.want)
			}
			if tc.want && model != "gpt-fork" {
				t.Fatalf("recovered model = %q, want gpt-fork", model)
			}
			if got := string(record.Metadata.WorldState); got != `{"alpha":"beta"}` {
				t.Fatalf("world state = %s, want {\"alpha\":\"beta\"}", got)
			}
		})
	}
}

func TestRecordFromPathIgnoresBareTurnContextBaselineLikeRust(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	recorder, err := NewRecorder(&CreateParams{
		CodexHome: t.TempDir(),
		ThreadID:  "thread-bare-context",
		Now:       now,
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	if err := recorder.AppendTurnContext(TurnContextRecord{TurnID: "turn-1", Model: "gpt-bare"}, now); err != nil {
		t.Fatalf("AppendTurnContext() error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	record, err := RecordFromPath(recorder.Path(), false)
	if err != nil {
		t.Fatalf("RecordFromPath() error = %v", err)
	}
	if _, _, _, ok := TurnContextSettings(record.Metadata.TurnContext); ok {
		t.Fatalf("bare turn context seeded a baseline: %s", record.Metadata.TurnContext)
	}
	if strings.TrimSpace(record.Metadata.Model) != "gpt-bare" {
		t.Fatalf("recovered model = %q, want the projected turn-context model", record.Metadata.Model)
	}
}

// Rust #41901: paginated rollouts can contain repeated wake turns with no user
// message boundary. A full world-state snapshot after the turn's latest
// compaction, paired with that turn's context, is still a durable baseline, so
// the newest empty wake turn supplies resume settings and world state.
func TestReconstructContextBaselineUsesEmptyWakeTurnSnapshotsLikeRust(t *testing.T) {
	lines := []Line{
		contextBaselineEventLine(t, "turn_started", "user-turn"),
		contextBaselineUserItemLine(t, "keep working"),
		contextBaselineTurnContextLine(t, "user-turn", "initial-model"),
		contextBaselineWorldStateLine(t, true, map[string]any{"environment": map[string]any{"status": "ready", "window": 0}}),
		contextBaselineEventLine(t, "turn_complete", "user-turn"),
	}
	for window := 1; window <= 3; window++ {
		turnID := fmt.Sprintf("wake-%d", window)
		lines = append(lines,
			contextBaselineEventLine(t, "turn_started", turnID),
			contextBaselineCompactedLine(t),
			contextBaselineWorldStateLine(t, true, map[string]any{"environment": map[string]any{"status": "starting", "window": window}}),
			contextBaselineTurnContextLine(t, turnID, fmt.Sprintf("model-%d", window)),
			contextBaselineWorldStateLine(t, false, map[string]any{"environment": map[string]any{"status": "ready"}}),
			contextBaselineEventLine(t, "turn_complete", turnID),
		)
	}

	baseline := ReconstructContextBaseline(lines)
	assertContextBaselineTurnContext(t, baseline, "model-3", true)
	if got := string(baseline.WorldState); got != `{"environment":{"status":"ready","window":3}}` {
		t.Fatalf("world state = %s, want the newest empty wake turn baseline", got)
	}
}
