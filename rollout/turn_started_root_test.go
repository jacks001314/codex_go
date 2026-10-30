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

// TestAppendTurnTerminalCarriesRoot covers Rust #51415: completion and
// interruption rollout lines now carry the resolved causal root, while callers
// without one keep the legacy shape.
func TestAppendTurnTerminalCarriesRoot(t *testing.T) {
	home := t.TempDir()
	recorder, err := NewRecorder(&CreateParams{CodexHome: home, ThreadID: "thread-root", HistoryMode: "paginated"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1700000000, 0).UTC()
	if err := recorder.AppendTurnCompleteWithRoot("root-turn", "completed-turn", now, 1000); err != nil {
		t.Fatal(err)
	}
	if err := recorder.AppendTurnComplete("legacy-completed", now, 1000); err != nil {
		t.Fatal(err)
	}
	if err := recorder.AppendTurnAbortedWithRoot("root-turn", "aborted-turn", "interrupted", now, 1000); err != nil {
		t.Fatal(err)
	}
	if err := recorder.AppendTurnAborted("legacy-aborted", "interrupted", now, 1000); err != nil {
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
	payloads := map[string]map[string]any{}
	for _, line := range lines {
		if line.Type != "event_msg" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(line.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		payloads[payload["turn_id"].(string)] = payload
	}
	if got := payloads["completed-turn"]["root_turn_id"]; got != "root-turn" {
		t.Fatalf("task_complete root = %#v", got)
	}
	if got := payloads["aborted-turn"]["root_turn_id"]; got != "root-turn" {
		t.Fatalf("turn_aborted root = %#v", got)
	}
	for _, turnID := range []string{"legacy-completed", "legacy-aborted"} {
		if _, present := payloads[turnID]["root_turn_id"]; present {
			t.Fatalf("legacy %s should omit root_turn_id: %#v", turnID, payloads[turnID])
		}
	}
}

// TestReplayBuilderCarriesRootFromTerminalEvents covers Rust #51415's history
// projection: a terminal event's root fills in when turn_started lacked one
// (older or synthetic writers), and a missing root stays unknown.
func TestReplayBuilderCarriesRootFromTerminalEvents(t *testing.T) {
	builder := newRolloutReplayBuilder(time.Unix(1700000000, 0).UTC())
	root := "root-turn"
	builder.handleTurnStarted(rolloutEventPayload{TurnID: "completed-turn"}, 0)
	builder.handleTurnComplete(rolloutEventPayload{TurnID: "completed-turn", RootTurnID: &root})
	builder.handleTurnStarted(rolloutEventPayload{TurnID: "aborted-turn"}, 1)
	builder.handleTurnAborted(rolloutEventPayload{TurnID: "aborted-turn", RootTurnID: &root}, 2)
	builder.handleTurnStarted(rolloutEventPayload{TurnID: "legacy-turn"}, 3)
	builder.handleTurnComplete(rolloutEventPayload{TurnID: "legacy-turn"})
	_, turns := builder.finish()
	if len(turns) != 3 {
		t.Fatalf("turns = %#v", turns)
	}
	if turns[0].ID != "completed-turn" || turns[0].RootTurnID != "root-turn" {
		t.Fatalf("completed turn = %#v", turns[0])
	}
	if turns[1].ID != "aborted-turn" || turns[1].RootTurnID != "root-turn" || turns[1].Status != "interrupted" {
		t.Fatalf("aborted turn = %#v", turns[1])
	}
	if turns[2].ID != "legacy-turn" || turns[2].RootTurnID != "" {
		t.Fatalf("legacy turn = %#v", turns[2])
	}
}
