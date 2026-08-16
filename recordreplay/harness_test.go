package recordreplay

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"codex_go/rollout"
)

// This file generalizes the L2 record-replay harness beyond the single
// oss-story.jsonl fixture (which only carries reasoning + agent_message
// surfaces and fully completed tasks). The synthetic traces below exercise
// trace shapes the recording does not cover - plain "message" surfaces,
// reasoning-only tasks, and an in-progress task whose recording stopped
// before task_complete - and prove the harness drives them through the Go
// recorder and app-server core with the same observable-contract checks.

// syntheticTaskEvents builds a normalized trace of one task lifecycle from a
// msg-type script. "task_started" starts the task, "task_complete" ends it;
// any other msg type is a per-task surface marker.
func syntheticTaskEvents(id string, msgTypes ...string) []Event {
	events := make([]Event, 0, len(msgTypes)+2)
	events = append(events, Event{Dir: "to_tui", Kind: "codex_event", ID: id, MsgType: "task_started"})
	for _, msgType := range msgTypes {
		if msgType == "task_started" || msgType == "task_complete" {
			continue
		}
		events = append(events, Event{Dir: "to_tui", Kind: "codex_event", ID: id, MsgType: msgType})
	}
	events = append(events, Event{Dir: "to_tui", Kind: "codex_event", ID: id, MsgType: "task_complete"})
	return events
}

// TestReplayThroughCoreSyntheticItemSurfaces drives synthetic traces whose
// per-task item surfaces differ from oss-story.jsonl through the app-server
// core: a reasoning-only task, a plain-"message" task (no reasoning), and a
// reasoning + agent_message task. The generalized scripted agent reproduces
// each surface and the cross-check verifies the observable contract
// (notification stream + persisted paginated rollout).
func TestReplayThroughCoreSyntheticItemSurfaces(t *testing.T) {
	var events []Event
	events = append(events,
		syntheticTaskEvents("task-reasoning-only", "agent_reasoning_raw_content_delta")...)
	events = append(events,
		syntheticTaskEvents("task-message-only", "message")...)
	events = append(events,
		syntheticTaskEvents("task-full", "agent_reasoning_raw_content_delta", "agent_message")...)

	lifecycles := TaskLifecycles(events)
	if len(lifecycles) != 3 {
		t.Fatalf("synthetic task lifecycles = %d, want 3", len(lifecycles))
	}

	result, err := ReplayThroughCore(events, t.TempDir())
	if err != nil {
		t.Fatalf("ReplayThroughCore(synthetic surfaces): %v", err)
	}
	if err := ValidateCoreReplayCrossCheck(events, result); err != nil {
		t.Fatalf("synthetic surfaces cross-check: %v", err)
	}
	if len(result.TurnIDs) != 3 {
		t.Fatalf("synthetic surfaces turns = %d, want 3", len(result.TurnIDs))
	}

	// The plain-message task must produce an agent_message item/completed with
	// text presence (the generalized agent mapping), not an empty turn.
	var messageCompleted bool
	for _, notification := range result.Notifications {
		if normalizeItemType(notification.ItemType) == "agent_message" && notification.HasText {
			messageCompleted = true
		}
	}
	if !messageCompleted {
		t.Fatalf("synthetic surfaces: no agent_message item/completed with text in notifications: %#v", result.Notifications)
	}
}

// TestReplayThroughCoreInProgressTaskReplaysToCompletion covers a trace whose
// second task recording stopped before task_complete (session ended
// mid-task). The harness must still drive the turn to completion - a recorded
// truncation must not lose the turn or corrupt the persisted lifecycle.
func TestReplayThroughCoreInProgressTaskReplaysToCompletion(t *testing.T) {
	var events []Event
	events = append(events, syntheticTaskEvents("task-1", "agent_message")...)
	// task-2 starts and emits reasoning deltas, but the recording ends before
	// task_complete.
	events = append(events, Event{Dir: "to_tui", Kind: "codex_event", ID: "task-2", MsgType: "task_started"})
	events = append(events, Event{Dir: "to_tui", Kind: "codex_event", ID: "task-2", MsgType: "agent_reasoning_raw_content_delta"})

	result, err := ReplayThroughCore(events, t.TempDir())
	if err != nil {
		t.Fatalf("ReplayThroughCore(in-progress): %v", err)
	}
	if err := ValidateCoreReplayCrossCheck(events, result); err != nil {
		t.Fatalf("in-progress cross-check: %v", err)
	}
	if len(result.TurnIDs) != 2 {
		t.Fatalf("in-progress turns = %d, want 2 (truncated task still replays)", len(result.TurnIDs))
	}

	// Recoverability holds after the truncated trace replay.
	resumed, err := rollout.Resume(result.RolloutPath)
	if err != nil {
		t.Fatalf("Resume after in-progress replay: %v", err)
	}
	if err := resumed.AppendTurnStarted("task-9", time.Unix(1700000000, 0).UTC()); err != nil {
		t.Fatalf("resumed AppendTurnStarted: %v", err)
	}
	if err := resumed.Close(); err != nil {
		t.Fatalf("resumed Close: %v", err)
	}
}

// TestReplayRecorderLevelPersistsUncompletedTurnWithoutFabricatedComplete
// pins the storage-side contract for an uncompleted task: the recorder must
// persist the open turn's task_started + item lines without fabricating a
// task_complete, and the rollout must resume cleanly.
func TestReplayRecorderLevelPersistsUncompletedTurnWithoutFabricatedComplete(t *testing.T) {
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome:   t.TempDir(),
		SessionID:   "synthetic-session",
		ThreadID:    "synthetic-thread",
		HistoryMode: "paginated",
		Source:      "recordreplay-harness",
	})
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}
	now := time.Unix(1700000000, 0).UTC()
	if err := recorder.AppendTurnStarted("task-open", now); err != nil {
		t.Fatalf("AppendTurnStarted: %v", err)
	}
	item, err := json.Marshal(rollout.Item{ID: "item-open", Type: "message", Role: "assistant", Text: "<omitted>"})
	if err != nil {
		t.Fatalf("Marshal item: %v", err)
	}
	if err := recorder.AppendItemStarted(item, "task-open", now); err != nil {
		t.Fatalf("AppendItemStarted: %v", err)
	}
	if err := recorder.AppendItemCompleted(item, "task-open", now, now); err != nil {
		t.Fatalf("AppendItemCompleted: %v", err)
	}
	// No AppendTurnComplete: the recording stopped mid-task.
	path := recorder.Path()
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close with open turn: %v", err)
	}

	lines, _, err := rollout.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var started, completed int
	for _, line := range lines {
		if line.Type != "event_msg" || len(line.Payload) == 0 {
			continue
		}
		var payload struct {
			Type   string `json:"type"`
			TurnID string `json:"turn_id"`
		}
		if err := json.Unmarshal(line.Payload, &payload); err != nil {
			continue
		}
		if payload.TurnID != "task-open" {
			continue
		}
		switch payload.Type {
		case "task_started":
			started++
		case "task_complete":
			completed++
		}
	}
	if started != 1 {
		t.Fatalf("open turn task_started lines = %d, want 1", started)
	}
	if completed != 0 {
		t.Fatalf("open turn fabricated %d task_complete lines, want 0", completed)
	}

	resumed, err := rollout.Resume(path)
	if err != nil {
		t.Fatalf("Resume after open turn: %v", err)
	}
	if err := resumed.AppendTurnStarted("task-9", now); err != nil {
		t.Fatalf("resumed AppendTurnStarted: %v", err)
	}
	if err := resumed.Close(); err != nil {
		t.Fatalf("resumed Close: %v", err)
	}
}

// TestTaskLifecyclesExcludeSessionOnlyIds pins the lifecycle extraction rule:
// ids that only carry session-level messages (session_configured,
// shutdown_complete) have no turn lifecycle and must not enter the replay set.
func TestTaskLifecyclesExcludeSessionOnlyIds(t *testing.T) {
	events := []Event{
		{Dir: "to_tui", Kind: "codex_event", ID: "session-0", MsgType: "session_configured"},
		{Dir: "to_tui", Kind: "codex_event", ID: "task-1", MsgType: "task_started"},
		{Dir: "to_tui", Kind: "codex_event", ID: "task-1", MsgType: "agent_message"},
		{Dir: "to_tui", Kind: "codex_event", ID: "task-1", MsgType: "task_complete"},
		{Dir: "to_tui", Kind: "codex_event", ID: "session-0", MsgType: "shutdown_complete"},
	}
	lifecycles := TaskLifecycles(events)
	if len(lifecycles) != 1 || lifecycles[0].ID != "task-1" {
		t.Fatalf("session-only ids leaked into lifecycles: %#v", lifecycles)
	}
	if !lifecycles[0].HasComplete {
		t.Fatalf("task-1 lifecycle missing completion")
	}
	if strings.Join(lifecycles[0].MsgTypes, ",") != "task_started,agent_message,task_complete" {
		t.Fatalf("task-1 msg types = %v", lifecycles[0].MsgTypes)
	}
}
