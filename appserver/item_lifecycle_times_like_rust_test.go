package appserver

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"codex_go/session"
	"codex_go/state"
	"codex_go/tool"
	"codex_go/turn"
)

func TestThreadItemLifecycleTimestampsSerializeLikeRust(t *testing.T) {
	encoded, err := json.Marshal(ThreadItem{ID: "item-1", Type: "agentMessage", Text: "hi"})
	if err != nil {
		t.Fatalf("marshal untimed item: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatalf("unmarshal untimed item: %v", err)
	}
	started, hasStarted := wire["startedAtMs"]
	if !hasStarted || started != nil {
		t.Fatalf("untimed item startedAtMs = %#v (present=%v), want explicit null", started, hasStarted)
	}
	completed, hasCompleted := wire["completedAtMs"]
	if !hasCompleted || completed != nil {
		t.Fatalf("untimed item completedAtMs = %#v (present=%v), want explicit null", completed, hasCompleted)
	}

	startedMS, completedMS := int64(1_700_000_000_000), int64(1_700_000_001_250)
	encoded, err = json.Marshal(ThreadItem{ID: "item-2", Type: "commandExecution", StartedAtMS: &startedMS, CompletedAtMS: &completedMS})
	if err != nil {
		t.Fatalf("marshal timed item: %v", err)
	}
	var decoded ThreadItem
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal timed item: %v", err)
	}
	if decoded.StartedAtMS == nil || *decoded.StartedAtMS != startedMS {
		t.Fatalf("decoded startedAtMs = %#v, want %d", decoded.StartedAtMS, startedMS)
	}
	if decoded.CompletedAtMS == nil || *decoded.CompletedAtMS != completedMS {
		t.Fatalf("decoded completedAtMs = %#v, want %d", decoded.CompletedAtMS, completedMS)
	}
	if _, ok := decoded.Data["startedAtMs"]; ok {
		t.Fatalf("startedAtMs leaked into data: %#v", decoded.Data)
	}
}

func TestBuildThreadItemLifecycleTimesFromMetadata(t *testing.T) {
	threadItem := BuildThreadItem(session.Item{
		ID:   "call-1",
		Type: "command_execution",
		Metadata: map[string]any{
			"startedAtMs":     int64(1_700_000_000_000),
			"completed_at_ms": int64(1_700_000_001_250),
		},
	})
	if threadItem.StartedAtMS == nil || *threadItem.StartedAtMS != 1_700_000_000_000 {
		t.Fatalf("startedAtMs = %#v", threadItem.StartedAtMS)
	}
	if threadItem.CompletedAtMS == nil || *threadItem.CompletedAtMS != 1_700_000_001_250 {
		t.Fatalf("completedAtMs = %#v", threadItem.CompletedAtMS)
	}

	// Older rollouts recorded zero for an unknown time.
	legacy := BuildThreadItem(session.Item{
		ID:       "call-2",
		Type:     "command_execution",
		Metadata: map[string]any{"startedAtMs": int64(0), "completedAtMs": int64(0)},
	})
	if legacy.StartedAtMS != nil || legacy.CompletedAtMS != nil {
		t.Fatalf("legacy zero times = %#v/%#v, want nil", legacy.StartedAtMS, legacy.CompletedAtMS)
	}
}

func TestDeserializeStateThreadItemAppliesLifecycleColumns(t *testing.T) {
	startedMS, completedMS := int64(1_700_000_000_000), int64(1_700_000_001_250)
	payload, err := json.Marshal(ThreadItem{ID: "agent-1", Type: "agentMessage", Text: "done"})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	item, err := deserializeStateThreadItem(state.ThreadHistoryItem{
		TurnID:        "turn-1",
		ItemID:        "agent-1",
		ItemJSON:      payload,
		StartedAtMS:   &startedMS,
		CompletedAtMS: &completedMS,
	})
	if err != nil {
		t.Fatalf("deserializeStateThreadItem() error = %v", err)
	}
	if item.StartedAtMS == nil || *item.StartedAtMS != startedMS {
		t.Fatalf("startedAtMs = %#v, want %d", item.StartedAtMS, startedMS)
	}
	if item.CompletedAtMS == nil || *item.CompletedAtMS != completedMS {
		t.Fatalf("completedAtMs = %#v, want %d", item.CompletedAtMS, completedMS)
	}

	untimed, err := deserializeStateThreadItem(state.ThreadHistoryItem{TurnID: "turn-1", ItemID: "agent-1", ItemJSON: payload})
	if err != nil {
		t.Fatalf("deserializeStateThreadItem(untimed) error = %v", err)
	}
	if untimed.StartedAtMS != nil || untimed.CompletedAtMS != nil {
		t.Fatalf("untimed item = %#v/%#v, want nil", untimed.StartedAtMS, untimed.CompletedAtMS)
	}
}

func TestRuntimeRouterItemNotificationsCarryLifecycleTimestamps(t *testing.T) {
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{})
	defer router.Close()
	router.SetNotificationSink(sink)
	startedAt := time.Now().UTC()
	finishedAt := startedAt.Add(time.Second)
	invocation := &tool.Invocation{
		CallID:   "timed-command",
		ToolName: tool.PlainName(tool.DefaultExecCommandToolName),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"cmd":"echo timed"}`},
		Source:   "code_mode",
	}
	router.runtimeToolStartedNotifier("thread-timed", "turn-timed", t.TempDir(), false)(context.Background(), invocation, startedAt)
	router.runtimeToolCompletedNotifier("thread-timed", "turn-timed", t.TempDir(), false)(context.Background(), &turn.ToolExecutionResult{
		Invocation: invocation,
		Output:     &tool.Output{Success: true, Body: "timed\n", Data: map[string]any{"exit_code": 0}},
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
	})

	notifications := sink.List()
	if len(notifications) < 2 {
		t.Fatalf("notifications = %#v", notifications)
	}
	started, ok := notifications[0].Params.(*ItemStartedNotification)
	if !ok {
		t.Fatalf("item started params = %#v", notifications[0].Params)
	}
	if started.Item["startedAtMs"] != startedAt.UnixMilli() {
		t.Fatalf("started item startedAtMs = %#v, want %d", started.Item["startedAtMs"], startedAt.UnixMilli())
	}
	if value, ok := started.Item["completedAtMs"]; !ok || value != nil {
		t.Fatalf("started item completedAtMs = %#v (present=%v), want explicit null", value, ok)
	}
	completed, ok := notifications[1].Params.(*ItemCompletedNotification)
	if !ok {
		t.Fatalf("item completed params = %#v", notifications[1].Params)
	}
	if completed.Item["completedAtMs"] != finishedAt.UnixMilli() {
		t.Fatalf("completed item completedAtMs = %#v, want %d", completed.Item["completedAtMs"], finishedAt.UnixMilli())
	}
	if completed.Item["startedAtMs"] != startedAt.UnixMilli() {
		t.Fatalf("completed item startedAtMs = %#v, want %d", completed.Item["startedAtMs"], startedAt.UnixMilli())
	}
}
