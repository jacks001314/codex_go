package rollout

import (
	"encoding/json"
	"testing"
	"time"
)

// TestAppendTurnStartedWithRootPersistsAttribution covers Rust #44611: the
// persisted turn-start event carries the originating root turn ID, while
// callers without attribution keep writing the legacy shape.
func TestAppendTurnStartedWithRootPersistsAttribution(t *testing.T) {
	home := t.TempDir()
	recorder, err := NewRecorder(&CreateParams{CodexHome: home, ThreadID: "thread-root", HistoryMode: "paginated"})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Unix(1700000000, 0).UTC()
	if err := recorder.AppendTurnStartedWithRoot("root-turn", "child-turn", started); err != nil {
		t.Fatal(err)
	}
	if err := recorder.AppendTurnStarted("legacy-turn", started); err != nil {
		t.Fatal(err)
	}
	path := recorder.Path()
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	lines, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	var withRoot, withoutRoot map[string]any
	for _, line := range lines {
		if line.Type != "event_msg" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(line.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["type"] != "task_started" {
			continue
		}
		switch payload["turn_id"] {
		case "child-turn":
			withRoot = payload
		case "legacy-turn":
			withoutRoot = payload
		}
	}
	if withRoot == nil || withRoot["root_turn_id"] != "root-turn" {
		t.Fatalf("attributed turn-start payload = %#v", withRoot)
	}
	if withoutRoot == nil {
		t.Fatalf("legacy turn-start payload missing: %#v", lines)
	}
	if _, present := withoutRoot["root_turn_id"]; present {
		t.Fatalf("legacy turn-start should omit root_turn_id: %#v", withoutRoot)
	}
}

// TestReplayBuilderCarriesRootTurnAttribution verifies older records without
// attribution stay compatible while new records surface the root turn ID.
func TestReplayBuilderCarriesRootTurnAttribution(t *testing.T) {
	builder := newRolloutReplayBuilder(time.Unix(1700000000, 0).UTC())
	root := "root-turn"
	builder.handleTurnStarted(rolloutEventPayload{TurnID: "child-turn", RootTurnID: &root}, 0)
	builder.handleTurnComplete(rolloutEventPayload{TurnID: "child-turn"})
	builder.handleTurnStarted(rolloutEventPayload{TurnID: "legacy-turn"}, 1)
	builder.handleTurnComplete(rolloutEventPayload{TurnID: "legacy-turn"})
	_, turns := builder.finish()
	if len(turns) != 2 {
		t.Fatalf("turns = %#v", turns)
	}
	if turns[0].ID != "child-turn" || turns[0].RootTurnID != "root-turn" {
		t.Fatalf("attributed turn = %#v", turns[0])
	}
	if turns[1].ID != "legacy-turn" || turns[1].RootTurnID != "" {
		t.Fatalf("legacy turn = %#v", turns[1])
	}
}
